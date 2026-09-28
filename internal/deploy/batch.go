package deploy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

var (
	ErrBatchInvalid   = errors.New("deploy: invalid batch")
	ErrBatchNotFound  = errors.New("deploy: batch not found")
	ErrBatchConflict  = errors.New("deploy: idempotency key reused for another request")
	ErrBatchBusy      = errors.New("deploy: batch is running")
	ErrBatchNoTargets = errors.New("deploy: no matching batch targets")
)

type BatchRequestItem struct {
	ArtifactID string            `json:"artifactId"`
	ServerIDs  []string          `json:"serverIds"`
	Config     map[string]string `json:"deployConfig"`
}

type BatchInput struct {
	RunID          string             `json:"-"`
	IdempotencyKey string             `json:"idempotencyKey"`
	Items          []BatchRequestItem `json:"items"`
}

type BatchTarget struct {
	ID           string            `json:"id"`
	ItemIndex    int               `json:"itemIndex"`
	ArtifactID   string            `json:"artifactId"`
	ArtifactName string            `json:"artifactName"`
	ServerID     string            `json:"serverId"`
	ServerName   string            `json:"serverName"`
	Config       map[string]string `json:"deployConfig"`
	Status       string            `json:"status"`
	Message      string            `json:"message"`
	Attempt      int               `json:"attempt"`
	StartedAt    *string           `json:"startedAt,omitempty"`
	FinishedAt   *string           `json:"finishedAt,omitempty"`
}

type BatchResult struct {
	ID        string        `json:"id"`
	RunID     string        `json:"runId"`
	Status    string        `json:"status"`
	CreatedAt string        `json:"createdAt"`
	UpdatedAt string        `json:"updatedAt"`
	Items     []BatchTarget `json:"items"`
}

type BatchService interface {
	DeployBatch(context.Context, BatchInput) (*BatchResult, error)
	GetBatch(context.Context, string, string) (*BatchResult, error)
	ListBatches(context.Context, string) ([]BatchResult, error)
	RetryBatch(context.Context, string, string) (*BatchResult, error)
	ContinueBatch(context.Context, string, string) (*BatchResult, error)
}

type batchPlanTarget struct {
	index    int
	artifact run.Artifact
	server   *target.Server
	config   map[string]string
}

func (s *service) preflightBatch(ctx context.Context, in BatchInput) ([]batchPlanTarget, error) {
	if s.batchDB == nil || strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 128 || len(in.Items) == 0 || len(in.Items) > 50 {
		return nil, ErrBatchInvalid
	}
	rn, err := s.runs.Get(ctx, in.RunID)
	if err != nil {
		if errors.Is(err, run.ErrNotFound) {
			return nil, ErrRunNotFound
		}
		return nil, err
	}
	if rn.Status != run.StatusSuccess && rn.Status != run.StatusPartialFailed {
		return nil, ErrRunNotSuccessful
	}
	artifacts, err := s.runs.ListArtifacts(ctx, in.RunID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]run.Artifact, len(artifacts))
	for _, a := range artifacts {
		byID[a.ID] = a
	}
	servers := map[string]*target.Server{}
	paths := map[string][]string{}
	seen := map[string]bool{}
	plan := make([]batchPlanTarget, 0)
	for index, item := range in.Items {
		for key := range item.Config {
			switch key {
			case "path", "releaseBase", "keepReleases", "commandTimeoutSeconds", "connectTimeoutSeconds", "uploadIdleTimeoutSeconds", "uploadTimeoutSeconds":
			default:
				return nil, ErrBatchInvalid
			}
		}
		a, ok := byID[item.ArtifactID]
		if !ok || !releaseModeArtifact(a) || len(item.ServerIDs) == 0 {
			return nil, ErrBatchInvalid
		}
		if strings.TrimSpace(item.Config["path"]) == "" && strings.TrimSpace(item.Config["releaseBase"]) == "" {
			return nil, ErrBatchInvalid
		}
		if _, err := parseUploadPolicy(item.Config); err != nil {
			return nil, err
		}
		base := strings.TrimSpace(releaseBase(a, item.Config))
		if !path.IsAbs(base) || path.Clean(base) == "/" || strings.ContainsAny(base, "\x00\n\r") {
			return nil, ErrBatchInvalid
		}
		base = path.Clean(base)
		for _, serverID := range item.ServerIDs {
			identity := fmt.Sprintf("%d:%s", index, serverID)
			if seen[identity] {
				return nil, ErrBatchInvalid
			}
			seen[identity] = true
			srv := servers[serverID]
			if srv == nil {
				srv, err = s.targets.Get(ctx, serverID)
				if err != nil {
					if errors.Is(err, target.ErrNotFound) {
						return nil, ErrServerNotFound
					}
					return nil, err
				}
				servers[serverID] = srv
			}
			for _, previous := range paths[serverID] {
				if base == previous || strings.HasPrefix(base+"/", previous+"/") || strings.HasPrefix(previous+"/", base+"/") {
					return nil, ErrBatchInvalid
				}
			}
			paths[serverID] = append(paths[serverID], base)
			cfg := make(map[string]string, len(item.Config)+1)
			for k, v := range item.Config {
				cfg[k] = v
			}
			cfg["releaseBase"] = base
			plan = append(plan, batchPlanTarget{index: index, artifact: a, server: srv, config: cfg})
			if len(plan) > 50 {
				return nil, ErrBatchInvalid
			}
		}
	}
	return plan, nil
}

func (s *service) DeployBatch(ctx context.Context, in BatchInput) (*BatchResult, error) {
	if s.batchDB == nil {
		return nil, ErrBatchInvalid
	}
	payload, _ := json.Marshal(in.Items)
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	if existing, err := s.batchByKey(ctx, in.RunID, in.IdempotencyKey); err == nil {
		if existing.hash != digest {
			return nil, ErrBatchConflict
		}
		return s.GetBatch(ctx, in.RunID, existing.id)
	} else if !errors.Is(err, ErrBatchNotFound) {
		return nil, err
	}
	plan, err := s.preflightBatch(ctx, in)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.batchDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO deploy_batches (id, run_id, idempotency_key, request_hash, status, created_at, updated_at) VALUES (?, ?, ?, ?, 'running', ?, ?)`, id, in.RunID, in.IdempotencyKey, digest, now, now)
	if err != nil {
		_ = tx.Rollback()
		if existing, readErr := s.batchByKey(ctx, in.RunID, in.IdempotencyKey); readErr == nil {
			if existing.hash != digest {
				return nil, ErrBatchConflict
			}
			return s.GetBatch(ctx, in.RunID, existing.id)
		}
		return nil, err
	}
	for _, p := range plan {
		cfg, _ := json.Marshal(p.config)
		_, err = tx.ExecContext(ctx, `INSERT INTO deploy_batch_items (id, batch_id, item_index, artifact_id, artifact_name, server_id, server_name, config_json, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending')`, uuid.NewString(), id, p.index, p.artifact.ID, p.artifact.Name, p.server.ID, p.server.Name, string(cfg))
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.executeBatch(ctx, in.RunID, id, "pending")
}

type batchIdentity struct{ id, hash string }

func (s *service) batchByKey(ctx context.Context, runID, key string) (batchIdentity, error) {
	var result batchIdentity
	err := s.batchDB.QueryRowContext(ctx, `SELECT id, request_hash FROM deploy_batches WHERE run_id=? AND idempotency_key=?`, runID, key).Scan(&result.id, &result.hash)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrBatchNotFound
	}
	return result, err
}

func (s *service) GetBatch(ctx context.Context, runID, batchID string) (*BatchResult, error) {
	if s.batchDB == nil {
		return nil, ErrBatchNotFound
	}
	b := &BatchResult{Items: []BatchTarget{}}
	err := s.batchDB.QueryRowContext(ctx, `SELECT id, run_id, status, created_at, updated_at FROM deploy_batches WHERE id=? AND run_id=?`, batchID, runID).Scan(&b.ID, &b.RunID, &b.Status, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBatchNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.batchDB.QueryContext(ctx, `SELECT id, item_index, artifact_id, artifact_name, server_id, server_name, config_json, status, message, attempt, started_at, finished_at FROM deploy_batch_items WHERE batch_id=? ORDER BY item_index, id`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item BatchTarget
		var config string
		var started, finished sql.NullString
		if err := rows.Scan(&item.ID, &item.ItemIndex, &item.ArtifactID, &item.ArtifactName, &item.ServerID, &item.ServerName, &config, &item.Status, &item.Message, &item.Attempt, &started, &finished); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(config), &item.Config); err != nil {
			return nil, err
		}
		if started.Valid {
			item.StartedAt = &started.String
		}
		if finished.Valid {
			item.FinishedAt = &finished.String
		}
		b.Items = append(b.Items, item)
	}
	return b, rows.Err()
}

func (s *service) ListBatches(ctx context.Context, runID string) ([]BatchResult, error) {
	if s.batchDB == nil {
		return nil, ErrBatchNotFound
	}
	rows, err := s.batchDB.QueryContext(ctx, `SELECT id FROM deploy_batches WHERE run_id=? ORDER BY created_at DESC LIMIT 50`, runID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	results := make([]BatchResult, 0, len(ids))
	for _, id := range ids {
		b, err := s.GetBatch(ctx, runID, id)
		if err != nil {
			return nil, err
		}
		results = append(results, *b)
	}
	return results, nil
}

func (s *service) RetryBatch(ctx context.Context, runID, batchID string) (*BatchResult, error) {
	return s.restartBatch(ctx, runID, batchID, "failed")
}

func (s *service) ContinueBatch(ctx context.Context, runID, batchID string) (*BatchResult, error) {
	return s.restartBatch(ctx, runID, batchID, "pending")
}

func (s *service) restartBatch(ctx context.Context, runID, batchID, selected string) (*BatchResult, error) {
	if s.batchDB == nil {
		return nil, ErrBatchNotFound
	}
	b, err := s.GetBatch(ctx, runID, batchID)
	if err != nil {
		return nil, err
	}
	count := 0
	for _, item := range b.Items {
		if batchTargetSelected(item.Status, selected) {
			count++
		}
	}
	if count == 0 {
		return nil, ErrBatchNoTargets
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.batchDB.ExecContext(ctx, `UPDATE deploy_batches SET status='running', updated_at=? WHERE id=? AND run_id=? AND status<>'running'`, now, batchID, runID)
	if err != nil {
		return nil, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil, ErrBatchBusy
	}
	return s.executeBatch(ctx, runID, batchID, selected)
}

func batchTargetSelected(status, selected string) bool {
	return status == selected || (selected == "failed" && status == run.TargetRolledBack)
}

func (s *service) executeBatch(ctx context.Context, runID, batchID, selected string) (result *BatchResult, err error) {
	defer func() {
		if err != nil {
			_, _ = s.finalizeBatch(ctx, runID, batchID)
		}
	}()
	b, err := s.GetBatch(ctx, runID, batchID)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.runs.ListArtifacts(ctx, runID)
	if err != nil {
		return nil, err
	}
	byID := map[string]run.Artifact{}
	for _, a := range artifacts {
		byID[a.ID] = a
	}
	groups := map[int][]BatchTarget{}
	indexes := []int{}
	for _, item := range b.Items {
		if !batchTargetSelected(item.Status, selected) {
			continue
		}
		if _, ok := groups[item.ItemIndex]; !ok {
			indexes = append(indexes, item.ItemIndex)
		}
		groups[item.ItemIndex] = append(groups[item.ItemIndex], item)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		failed := false
		for _, item := range groups[index] {
			if ctx.Err() != nil {
				break
			}
			a, ok := byID[item.ArtifactID]
			if !ok {
				failed = true
				if err := s.finishBatchTarget(ctx, item.ID, "failed", "产物已不可用", time.Now().UTC()); err != nil {
					return nil, err
				}
				continue
			}
			srv, err := s.targets.Get(ctx, item.ServerID)
			if err != nil {
				failed = true
				if err := s.finishBatchTarget(ctx, item.ID, "failed", "目标服务器已不可用", time.Now().UTC()); err != nil {
					return nil, err
				}
				continue
			}
			configured, err := withUploadPolicy(ctx, item.Config)
			if err != nil {
				failed = true
				if err := s.finishBatchTarget(ctx, item.ID, "failed", "部署配置无效", time.Now().UTC()); err != nil {
					return nil, err
				}
				continue
			}
			started := time.Now().UTC()
			if _, err := s.batchDB.ExecContext(ctx, `UPDATE deploy_batch_items SET status='running', attempt=attempt+1, started_at=?, finished_at=NULL, message='' WHERE id=?`, started.Format(time.RFC3339Nano), item.ID); err != nil {
				return nil, err
			}
			result := s.deployOne(configured, srv, a, item.Config, nil)
			finished := time.Now().UTC()
			if result.Status != run.TargetSuccess {
				failed = true
			}
			if err := s.finishBatchTarget(ctx, item.ID, result.Status, result.Message, finished); err != nil {
				return nil, err
			}
		}
		if failed || ctx.Err() != nil {
			break
		}
	}
	return s.finalizeBatch(ctx, runID, batchID)
}

func (s *service) finishBatchTarget(ctx context.Context, id, status, message string, finished time.Time) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	_, err := s.batchDB.ExecContext(cleanup, `UPDATE deploy_batch_items SET status=?, message=?, finished_at=? WHERE id=?`, status, truncate(message), finished.Format(time.RFC3339Nano), id)
	return err
}

func (s *service) finalizeBatch(ctx context.Context, runID, batchID string) (*BatchResult, error) {
	canceled := ctx.Err() != nil
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	b, err := s.GetBatch(ctx, runID, batchID)
	if err != nil {
		return nil, err
	}
	success, failed, pending := 0, 0, 0
	for _, item := range b.Items {
		switch item.Status {
		case run.TargetSuccess:
			success++
		case "pending":
			pending++
		default:
			failed++
		}
	}
	status := "success"
	if pending > 0 && canceled {
		status = "canceled"
	} else if (failed > 0 || pending > 0) && success > 0 {
		status = "partial_failed"
	} else if failed > 0 || pending > 0 {
		status = "failed"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.batchDB.ExecContext(ctx, `UPDATE deploy_batches SET status=?, updated_at=? WHERE id=?`, status, now, batchID); err != nil {
		return nil, err
	}
	if failed > 0 {
		terminal := run.StatusFailed
		if success > 0 {
			terminal = run.StatusPartialFailed
		}
		if err := s.runs.SetDeployTerminal(ctx, runID, terminal); err != nil {
			return nil, err
		}
	}
	return s.GetBatch(ctx, runID, batchID)
}

// RecoverInterruptedBatches is called once at startup; it never replays remote commands.
func (s *service) RecoverInterruptedBatches(ctx context.Context) error {
	if s.batchDB == nil {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.batchDB.ExecContext(ctx, `UPDATE deploy_batch_items SET status='failed', message='服务重启中断,请手动重试', finished_at=? WHERE status='running'`, now); err != nil {
		return err
	}
	rows, err := s.batchDB.QueryContext(ctx, `SELECT id, run_id FROM deploy_batches WHERE status='running'`)
	if err != nil {
		return err
	}
	type unfinished struct{ id, runID string }
	var pending []unfinished
	for rows.Next() {
		var item unfinished
		if err := rows.Scan(&item.id, &item.runID); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range pending {
		if _, err := s.finalizeBatch(ctx, item.runID, item.id); err != nil {
			return err
		}
	}
	return nil
}
