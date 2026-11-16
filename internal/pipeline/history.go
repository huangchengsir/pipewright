package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
)

const (
	HistorySourceCanvas   = "canvas"
	HistorySourceImport   = "yaml_import"
	HistorySourceTemplate = "template"
	HistorySourceAI       = "ai"
	HistorySourceMCP      = "mcp"
	historySourceBaseline = "baseline"
)

var (
	ErrHistoryNotFound         = errors.New("pipeline: history version not found")
	ErrHistoryDraftUnavailable = errors.New("pipeline: historical draft unavailable")
)

type HistoryCipher interface {
	SealSecret([]byte) ([]byte, error)
	OpenSecret([]byte) ([]byte, error)
}

type historyAuthor struct {
	actor       string
	source      string
	legacyAdmin bool
}

type historyAuthorKey struct{}

// WithHistoryAuthor carries provenance derived by a trusted adapter.
func WithHistoryAuthor(ctx context.Context, actor, source string) context.Context {
	return context.WithValue(ctx, historyAuthorKey{}, historyAuthor{actor: actor, source: source})
}

// WithLegacyHistoryAuthor identifies a verified single-administrator session.
// The committed writer resolves the account inside the same transaction.
func WithLegacyHistoryAuthor(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, historyAuthorKey{}, historyAuthor{source: source, legacyAdmin: true})
}

type HistoryVersion struct {
	ID          string `json:"id"`
	Sequence    int64  `json:"sequence"`
	CreatedAt   string `json:"createdAt"`
	Actor       string `json:"actor"`
	Source      string `json:"source"`
	ChangeCount int    `json:"changeCount"`
	Current     bool   `json:"current"`
}

type HistoryPage struct {
	Versions   []HistoryVersion `json:"versions"`
	NextBefore int64            `json:"nextBefore,omitempty"`
}

type HistoryDetail struct {
	HistoryVersion
	PreviousID       string          `json:"previousId,omitempty"`
	PreviousSequence int64           `json:"previousSequence,omitempty"`
	Changes          []HistoryChange `json:"changes"`
	DraftAvailable   bool            `json:"draftAvailable"`
}

// HistoryReader is implemented by the persistent pipeline service. Existing
// callers of Service do not need to provide history.
type HistoryReader interface {
	ListHistory(context.Context, string, int64, int) (HistoryPage, error)
	GetHistory(context.Context, string, string) (HistoryDetail, error)
	HistoryDraft(context.Context, string, string) (*Config, int64, error)
}

type historyEnvelope struct {
	ProjectID string          `json:"projectId"`
	VersionID string          `json:"versionId"`
	Spec      json.RawMessage `json:"spec"`
}

func WithHistoryCipher(cipher HistoryCipher) ServiceOption {
	return func(s *service) { s.historyCipher = cipher }
}

// StoreHistoryGraph is the shared committed-graph writer for the canvas and
// MCP apply. Call in the existing project write transaction while holding the
// project lock. Configuration, immutable versions, and pointer share a commit.
func StoreHistoryGraph(ctx context.Context, q configstate.DBTX, cipher HistoryCipher, projectID string, raw []byte, yaml, stamp string) error {
	var oldRaw string
	var current sql.NullString
	var oldSaved sql.NullString
	if err := q.QueryRowContext(ctx, "SELECT spec_json,history_version_id,saved_at FROM pipeline_configs WHERE project_id=?", projectID).Scan(&oldRaw, &current, &oldSaved); err != nil {
		return err
	}
	if strings.TrimSpace(oldRaw) == "" {
		oldRaw = "{}"
	}
	before, err := decodeHistoryGraph([]byte(oldRaw))
	if err != nil {
		return err
	}
	after, err := decodeHistoryGraph(raw)
	if err != nil {
		return err
	}
	changes := diffHistory(before, after)
	if len(changes) > 0 {
		var seq int64
		if current.Valid {
			if err := q.QueryRowContext(ctx, "SELECT version_seq FROM pipeline_config_versions WHERE project_id=? AND id=?", projectID, current.String).Scan(&seq); err != nil {
				return err
			}
		} else if oldSaved.Valid {
			// An existing graph has a known saved time but no recorded author.
			seq = 1
			if _, err := insertHistoryVersion(ctx, q, cipher, projectID, seq, []byte(oldRaw), oldSaved.String, historyAuthor{source: historySourceBaseline}, nil); err != nil {
				return err
			}
		}
		author, _ := ctx.Value(historyAuthorKey{}).(historyAuthor)
		if author.source == "" {
			author.source = HistorySourceCanvas
		}
		if author.legacyAdmin {
			err := q.QueryRowContext(ctx, "SELECT username FROM admin_user WHERE id=1").Scan(&author.actor)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		id, err := insertHistoryVersion(ctx, q, cipher, projectID, seq+1, raw, stamp, author, changes)
		if err != nil {
			return err
		}
		current = sql.NullString{String: id, Valid: true}
	}

	_, err = q.ExecContext(ctx, `UPDATE pipeline_configs
	 SET spec_json=?,spec_yaml=?,status=?,updated_at=?,saved_at=?,history_version_id=?
	 WHERE project_id=?`, string(raw), yaml, statusDraft, stamp, stamp, current, projectID)
	return err
}

func decodeHistoryGraph(raw []byte) (Spec, error) {
	var spec Spec
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&spec)
	return spec, err
}

func insertHistoryVersion(ctx context.Context, q configstate.DBTX, cipher HistoryCipher, projectID string, sequence int64, raw []byte, stamp string, author historyAuthor, changes []HistoryChange) (string, error) {
	id := uuid.NewString()
	payload, err := json.Marshal(changes)
	if err != nil {
		return "", err
	}
	var sealed []byte
	if cipher != nil {
		plain, err := json.Marshal(historyEnvelope{ProjectID: projectID, VersionID: id, Spec: raw})
		if err != nil {
			return "", err
		}
		defer clear(plain)
		sealed, err = cipher.SealSecret(plain)
		if err != nil && !errors.Is(err, vault.ErrVaultUnconfigured) {
			return "", err
		}
	}
	_, err = q.ExecContext(ctx, `INSERT INTO pipeline_config_versions
	 (id,project_id,version_seq,created_at,actor,source,change_count,changes_json,ciphertext)
	 VALUES (?,?,?,?,?,?,?,?,?)`, id, projectID, sequence, stamp, author.actor, author.source, len(changes), string(payload), sealed)
	return id, err
}

func (s *service) ListHistory(ctx context.Context, projectID string, before int64, limit int) (HistoryPage, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	page := HistoryPage{Versions: []HistoryVersion{}}
	err := configstate.WithReadTxContext(ctx, s.db, func(ctx context.Context, q configstate.DBTX) error {
		current, err := s.historyAccess(ctx, q, projectID, "pipeline.read")
		if err != nil {
			return err
		}
		query := `SELECT id,version_seq,created_at,actor,source,change_count
		 FROM pipeline_config_versions WHERE project_id=?`
		args := []any{projectID}
		if before > 0 {
			query += " AND version_seq < ?"
			args = append(args, before)
		}
		// SQL Server and DM use OFFSET/FETCH instead of LIMIT.
		query += " ORDER BY version_seq DESC" + store.Limit(store.DialectOf(s.db), strconv.Itoa(limit+1))
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row HistoryVersion
			if err := rows.Scan(&row.ID, &row.Sequence, &row.CreatedAt, &row.Actor, &row.Source, &row.ChangeCount); err != nil {
				return err
			}
			if len(page.Versions) == limit {
				page.NextBefore = page.Versions[len(page.Versions)-1].Sequence
				break
			}
			row.Current = row.ID == current
			page.Versions = append(page.Versions, row)
		}
		return rows.Err()
	})
	if err == nil {
		err = tenancy.Check(ctx, s.db, "pipeline.read", "project", projectID)
	}
	return page, err
}

func (s *service) GetHistory(ctx context.Context, projectID, versionID string) (HistoryDetail, error) {
	var detail HistoryDetail
	err := configstate.WithReadTxContext(ctx, s.db, func(ctx context.Context, q configstate.DBTX) error {
		current, err := s.historyAccess(ctx, q, projectID, "pipeline.read")
		if err != nil {
			return err
		}
		return s.loadHistoryDetail(ctx, q, projectID, versionID, current, &detail)
	})
	if err == nil {
		err = tenancy.Check(ctx, s.db, "pipeline.read", "project", projectID)
	}
	return detail, err
}

func (s *service) loadHistoryDetail(ctx context.Context, q configstate.DBTX, projectID, versionID, current string, detail *HistoryDetail) error {
	var encoded string
	var sealed []byte
	err := q.QueryRowContext(ctx, `SELECT id,version_seq,created_at,actor,source,change_count,changes_json,ciphertext
	 FROM pipeline_config_versions WHERE project_id=? AND id=?`, projectID, versionID).
		Scan(&detail.ID, &detail.Sequence, &detail.CreatedAt, &detail.Actor, &detail.Source, &detail.ChangeCount, &encoded, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrHistoryNotFound
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(encoded), &detail.Changes); err != nil {
		return err
	}
	if detail.Changes == nil {
		detail.Changes = []HistoryChange{}
	}
	detail.Current = detail.ID == current
	if detail.Sequence > 1 {
		detail.PreviousSequence = detail.Sequence - 1
		if err := q.QueryRowContext(ctx, `SELECT id FROM pipeline_config_versions WHERE project_id=? AND version_seq=?`, projectID, detail.PreviousSequence).Scan(&detail.PreviousID); err != nil {
			return err
		}
	}
	spec, ok := s.openHistorySpec(sealed, projectID, versionID)
	if ok {
		currentConfig, err := s.load(ctx, projectID)
		if err != nil {
			return err
		}
		detail.DraftAvailable = safeHistoricalDraft(spec, currentConfig.Spec) && authorizeConfigReferences(ctx, s.db, spec) == nil
	}
	return nil
}

func (s *service) historyAccess(ctx context.Context, q configstate.DBTX, projectID, action string) (string, error) {
	if err := tenancy.Check(ctx, s.db, action, "project", projectID); err != nil {
		return "", err
	}
	var current sql.NullString
	err := q.QueryRowContext(ctx, `SELECT pc.history_version_id FROM projects p
	 LEFT JOIN pipeline_configs pc ON pc.project_id=p.id WHERE p.id=?`, projectID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrProjectNotFound
	}
	return current.String, err
}

func (s *service) openHistorySpec(sealed []byte, projectID, versionID string) (Spec, bool) {
	if len(sealed) == 0 || s.historyCipher == nil {
		return Spec{}, false
	}
	plain, err := s.historyCipher.OpenSecret(sealed)
	if err != nil {
		return Spec{}, false
	}
	defer clear(plain)
	var envelope historyEnvelope
	err = json.Unmarshal(plain, &envelope)
	defer clear(envelope.Spec)
	if err != nil || envelope.ProjectID != projectID || envelope.VersionID != versionID {
		return Spec{}, false
	}
	// Unknown stage or graph fields cannot round-trip through the canvas DTO.
	// Keep the summary available rather than offer a partial restore.
	var spec Spec
	decoder := json.NewDecoder(bytes.NewReader(envelope.Spec))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&spec) != nil {
		return Spec{}, false
	}
	return spec, true
}

func (s *service) HistoryDraft(ctx context.Context, projectID, versionID string) (*Config, int64, error) {
	var draft *Config
	var revision int64
	err := configstate.WithReadTxContext(ctx, s.db, func(ctx context.Context, q configstate.DBTX) error {
		if _, err := s.historyAccess(ctx, q, projectID, "pipeline.read"); err != nil {
			return err
		}
		if _, err := s.historyAccess(ctx, q, projectID, "pipeline.edit"); err != nil {
			return err
		}
		var sealed []byte
		var created string
		err := q.QueryRowContext(ctx, "SELECT ciphertext,created_at FROM pipeline_config_versions WHERE project_id=? AND id=?", projectID, versionID).Scan(&sealed, &created)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHistoryNotFound
		}
		if err != nil {
			return err
		}
		spec, ok := s.openHistorySpec(sealed, projectID, versionID)
		if !ok {
			return ErrHistoryDraftUnavailable
		}
		if err := authorizeConfigReferences(ctx, s.db, spec); err != nil {
			return err
		}
		current, err := s.load(ctx, projectID)
		if err != nil {
			return err
		}
		if !safeHistoricalDraft(spec, current.Spec) {
			return ErrHistoryDraftUnavailable
		}
		yaml, err := renderYAML(spec)
		if err != nil {
			return err
		}
		updated, err := time.Parse(time.RFC3339, created)
		if err != nil {
			return err
		}
		revision, err = configstate.ReadRevision(ctx, q, projectID)
		if err != nil {
			return err
		}
		draft = &Config{Spec: spec, YAML: yaml, Status: statusDraft, UpdatedAt: updated}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	// Authorization may have changed after reading the encrypted snapshot.
	// Recheck the complete scope before returning editable configuration.
	err = configstate.WithReadTxContext(ctx, s.db, func(ctx context.Context, q configstate.DBTX) error {
		if _, err := s.historyAccess(ctx, q, projectID, "pipeline.read"); err != nil {
			return err
		}
		if err := tenancy.Check(ctx, s.db, "pipeline.edit", "project", projectID); err != nil {
			return err
		}
		now, err := configstate.ReadRevision(ctx, q, projectID)
		if err != nil {
			return err
		}
		if now != revision {
			return ErrHistoryDraftUnavailable
		}
		return authorizeConfigReferences(ctx, s.db, draft.Spec)
	})
	if err != nil {
		return nil, 0, err
	}
	return draft, revision, nil
}
