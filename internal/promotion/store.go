package promotion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Store 持久化环境链 / 环境变量 / 晋级记录(参数化 SQL)。
type Store struct {
	db *sql.DB
}

// NewStore 构造持久层。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// GetChain 取某项目的环境链。未配置 → ErrChainNotConfigured。
func (s *Store) GetChain(ctx context.Context, projectID string) (Chain, error) {
	var chainJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT chain_json FROM project_environments WHERE project_id = ?`, projectID).Scan(&chainJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Chain{}, ErrChainNotConfigured
		}
		return Chain{}, fmt.Errorf("promotion: get chain: %w", err)
	}
	var envs []EnvStage
	if strings.TrimSpace(chainJSON) != "" {
		if uerr := json.Unmarshal([]byte(chainJSON), &envs); uerr != nil {
			return Chain{}, fmt.Errorf("promotion: decode chain: %w", uerr)
		}
	}
	if len(envs) == 0 {
		return Chain{}, ErrChainNotConfigured
	}
	return Chain{Environments: envs}, nil
}

// SaveChain upsert 某项目的环境链(校验非空/无重名)。项目不存在 → ErrProjectNotFound。
func (s *Store) SaveChain(ctx context.Context, projectID string, c Chain) (Chain, error) {
	valid, err := validateChain(c)
	if err != nil {
		return Chain{}, err
	}
	raw, err := json.Marshal(valid.Environments)
	if err != nil {
		return Chain{}, fmt.Errorf("promotion: encode chain: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO project_environments (project_id, chain_json, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(project_id) DO UPDATE SET chain_json = excluded.chain_json, updated_at = excluded.updated_at`,
		projectID, string(raw), now,
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return Chain{}, ErrProjectNotFound
		}
		return Chain{}, fmt.Errorf("promotion: save chain: %w", err)
	}
	return valid, nil
}

// SetVariables 覆盖某项目某环境的全部作用域变量(整批替换;单事务)。
// secret 变量须给 credentialId(不存明文);非 secret 存明文 value。空 key → ErrVarKeyEmpty;
// 同环境内 key 重复 → ErrVarKeyDuplicate。项目不存在 → ErrProjectNotFound。
func (s *Store) SetVariables(ctx context.Context, projectID, env string, vars []Variable) error {
	env = strings.TrimSpace(env)
	if env == "" {
		return ErrInvalidEnvName
	}
	seen := map[string]struct{}{}
	for i := range vars {
		vars[i].Key = strings.TrimSpace(vars[i].Key)
		if vars[i].Key == "" {
			return ErrVarKeyEmpty
		}
		if _, dup := seen[vars[i].Key]; dup {
			return ErrVarKeyDuplicate
		}
		seen[vars[i].Key] = struct{}{}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("promotion: begin set vars tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM environment_variables WHERE project_id = ? AND environment = ?`,
		projectID, env); err != nil {
		return fmt.Errorf("promotion: clear vars: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, v := range vars {
		value, credID := v.Value, ""
		isSecret := 0
		if v.Secret {
			isSecret = 1
			value = "" // secret 绝不存明文
			credID = strings.TrimSpace(v.CredentialID)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO environment_variables
			   (id, project_id, environment, var_key, value, is_secret, credential_id, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), projectID, env, v.Key, value, isSecret, credID, now,
		); err != nil {
			if isForeignKeyErr(err) {
				return ErrProjectNotFound
			}
			return fmt.Errorf("promotion: insert var: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("promotion: commit set vars: %w", err)
	}
	return nil
}

// ListVariables 取某项目某环境的全部变量(掩码视图:secret 仅 credentialId,绝无明文)。
func (s *Store) ListVariables(ctx context.Context, projectID, env string) ([]Variable, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT var_key, value, is_secret, credential_id
		 FROM environment_variables WHERE project_id = ? AND environment = ?
		 ORDER BY var_key ASC`, projectID, env)
	if err != nil {
		return nil, fmt.Errorf("promotion: list vars: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Variable{}
	for rows.Next() {
		var (
			v        Variable
			isSecret int
		)
		if err := rows.Scan(&v.Key, &v.Value, &isSecret, &v.CredentialID); err != nil {
			return nil, fmt.Errorf("promotion: scan var: %w", err)
		}
		if isSecret == 1 {
			v.Secret = true
			v.Value = "" // 双保险:绝不外泄明文
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// rawVariable 是内部读形状(含 is_secret/credential_id)供变量解析注入。
type rawVariable struct {
	Key          string
	Value        string
	Secret       bool
	CredentialID string
}

// loadRawVariables 取某环境全部变量的内部形状(供 Resolver 解密注入)。
func (s *Store) loadRawVariables(ctx context.Context, projectID, env string) ([]rawVariable, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT var_key, value, is_secret, credential_id
		 FROM environment_variables WHERE project_id = ? AND environment = ?
		 ORDER BY var_key ASC`, projectID, env)
	if err != nil {
		return nil, fmt.Errorf("promotion: load raw vars: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []rawVariable{}
	for rows.Next() {
		var (
			v        rawVariable
			isSecret int
		)
		if err := rows.Scan(&v.Key, &v.Value, &isSecret, &v.CredentialID); err != nil {
			return nil, fmt.Errorf("promotion: scan raw var: %w", err)
		}
		v.Secret = isSecret == 1
		out = append(out, v)
	}
	return out, rows.Err()
}

// currentEnvForRun 返回某源运行已晋级到的「最高」环境(按链序)。
// 无任何 promoted 记录 → 空串(尚未晋级)。
func (s *Store) currentEnvForRun(ctx context.Context, runID string, c Chain) (string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT target_environment FROM run_promotions
		 WHERE source_run_id = ? AND status = ?`, runID, StatusPromoted)
	if err != nil {
		return "", fmt.Errorf("promotion: query promoted envs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	best := -1
	for rows.Next() {
		var env string
		if err := rows.Scan(&env); err != nil {
			return "", fmt.Errorf("promotion: scan promoted env: %w", err)
		}
		if i := c.IndexOf(env); i > best {
			best = i
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if best < 0 {
		return "", nil
	}
	return c.Environments[best].Name, nil
}

// hasActivePromotion 报告某源运行到某环境是否已有 pending/promoted 记录(防重复晋级/并发重入)。
func (s *Store) hasActivePromotion(ctx context.Context, runID, env string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM run_promotions
		 WHERE source_run_id = ? AND target_environment = ? AND status IN (?, ?)`,
		runID, env, StatusPending, StatusPromoted).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("promotion: check active promotion: %w", err)
	}
	return n > 0, nil
}

// createRecord 登记一条晋级记录(初始 status)。
func (s *Store) createRecord(ctx context.Context, rec Record) (string, error) {
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	decided := ""
	if rec.Status == StatusPromoted || rec.Status == StatusRejected {
		decided = now
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO run_promotions
		   (id, project_id, source_run_id, from_environment, target_environment,
		    status, approval_stage, promoted_by, created_at, decided_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, rec.ProjectID, rec.SourceRunID, rec.FromEnvironment, rec.TargetEnvironment,
		rec.Status, rec.ApprovalStage, rec.PromotedBy, now, decided,
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return "", ErrRunNotFound
		}
		return "", fmt.Errorf("promotion: create record: %w", err)
	}
	return id, nil
}

// decideRecord 把某记录置为终态(promoted/rejected)+ 记决定时刻 + 决定人。
func (s *Store) decideRecord(ctx context.Context, id, status, by string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE run_promotions SET status = ?, decided_at = ?, promoted_by = ? WHERE id = ?`,
		status, now, by, id)
	if err != nil {
		return fmt.Errorf("promotion: decide record: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("promotion: decide record: not found")
	}
	return nil
}

// ListRecordsForRun 返回某源运行的全部晋级记录(按 created_at 升序)。
func (s *Store) ListRecordsForRun(ctx context.Context, runID string) ([]Record, error) {
	return s.queryRecords(ctx, `WHERE source_run_id = ?`, runID)
}

// ListRecordsForProject 返回某项目的全部晋级记录(按 created_at 降序,最近在前)。
func (s *Store) ListRecordsForProject(ctx context.Context, projectID string) ([]Record, error) {
	return s.queryRecords(ctx, `WHERE project_id = ? ORDER BY created_at DESC, id DESC`, projectID)
}

func (s *Store) queryRecords(ctx context.Context, clause, arg string) ([]Record, error) {
	q := `SELECT id, project_id, source_run_id, from_environment, target_environment,
	             status, approval_stage, promoted_by, created_at, decided_at
	      FROM run_promotions ` + clause
	if !strings.Contains(clause, "ORDER BY") {
		q += " ORDER BY created_at ASC, id ASC"
	}
	rows, err := s.db.QueryContext(ctx, q, arg)
	if err != nil {
		return nil, fmt.Errorf("promotion: query records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Record{}
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.SourceRunID, &r.FromEnvironment, &r.TargetEnvironment,
			&r.Status, &r.ApprovalStage, &r.PromotedBy, &r.CreatedAt, &r.DecidedAt); err != nil {
			return nil, fmt.Errorf("promotion: scan record: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// isForeignKeyErr 判断错误是否为外键约束失败(modernc sqlite 文本含 FOREIGN KEY)。
func isForeignKeyErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY")
}
