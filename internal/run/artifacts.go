package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// artifacts.go 是「构建产物契约」的领域层(FR-6 / Story 3.4)。
//
// 产物契约是 Epic 3 一等交付物:type 枚举 + reference 寻址语义定死,Epic 4 部署按
// (type, reference) 消费,无需了解构建内部。3-3 真实构建落地后经同一 StepSink.EmitArtifact
// 接口喂产物,**契约形状不变**。

// 产物类型枚举(冻结;后续新增类型只加枚举不改形状)。
const (
	// ArtifactImage 表示容器镜像(reference = repo:tag 或本地 image id)。
	ArtifactImage = "image"
	// ArtifactJar 表示 JAR 包(reference = 路径/URL)。
	ArtifactJar = "jar"
	// ArtifactDist 表示前端构建产物目录(reference = 目录/tarball 寻址)。
	ArtifactDist = "dist"
	// ArtifactArchive 表示归档包(reference = 路径)。
	ArtifactArchive = "archive"
)

// isValidArtifactType 报告 type 是否为冻结枚举之一。
func isValidArtifactType(t string) bool {
	switch t {
	case ArtifactImage, ArtifactJar, ArtifactDist, ArtifactArchive:
		return true
	default:
		return false
	}
}

// Artifact 是一次运行产出的构建产物领域模型(对齐冻结 run-detail artifacts 子 DTO)。
//
//   - Type      : image | jar | dist | archive(冻结枚举)。
//   - Name      : 产物逻辑名(如服务名)。
//   - Reference : 类型寻址引用(Epic 4 按 (Type, Reference) 消费)。
//   - SizeBytes : 字节数(未知 = 0)。
//   - Metadata  : 自由 KV(digest/path/stub 等);nil 视为空。
type Artifact struct {
	ID        string
	RunID     string
	Type      string
	Name      string
	Reference string
	SizeBytes int64
	Metadata  map[string]any
	CreatedAt time.Time
}

// encodeMetadata 把产物 metadata 序列化为可入库 JSON(nil/空 → "{}")。
func encodeMetadata(m map[string]any) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeMetadata 把入库 JSON 反序列化为 map(空串/无效 → 空 map,绝不让坏数据致 List 失败)。
func decodeMetadata(s string) map[string]any {
	if strings.TrimSpace(s) == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// AddArtifact 持久化一条运行产物到 run_artifacts(参数化 SQL)。
// type 非法 → ErrInvalidArtifactType;run 不存在 → ErrNotFound(外键失败)。
// 由 dbStepSink.EmitArtifact / runner 报告路径调用;真实构建(3-3)经同一接口接入,契约不变。
func (s *service) AddArtifact(ctx context.Context, a Artifact) (*Artifact, error) {
	a.Type = strings.TrimSpace(a.Type)
	if !isValidArtifactType(a.Type) {
		return nil, ErrInvalidArtifactType
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	metaJSON, err := encodeMetadata(a.Metadata)
	if err != nil {
		return nil, fmt.Errorf("run: encode artifact metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO run_artifacts
		   (id, run_id, type, name, reference, size_bytes, metadata_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.RunID, a.Type, a.Name, a.Reference, a.SizeBytes, metaJSON,
		a.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("run: insert artifact: %w", err)
	}
	out := a
	out.CreatedAt = a.CreatedAt.UTC()
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
	return &out, nil
}

// ListArtifacts 取某次运行的全部产物(按 created_at 升序,**插入序(rowid)定 tiebreaker**;
// 无产物 → 空切片)。created_at 为 RFC3339 秒精度,同秒插入相等 → 用 rowid(插入序)而非随机
// UUID id 破并列,保证产物先后稳定确定。run 不存在不报错(返回空切片);HTTP 层据 run 存在性决定
// 404(仿 GetLogs 语义)。参数化 SQL。
func (s *service) ListArtifacts(ctx context.Context, runID string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, type, name, reference, size_bytes, metadata_json, created_at
		 FROM run_artifacts WHERE run_id = ? ORDER BY created_at ASC, rowid ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("run: load artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []Artifact{}
	for rows.Next() {
		var (
			a          Artifact
			metaJSON   string
			createdStr string
		)
		if err := rows.Scan(&a.ID, &a.RunID, &a.Type, &a.Name, &a.Reference,
			&a.SizeBytes, &metaJSON, &createdStr); err != nil {
			return nil, fmt.Errorf("run: scan artifact: %w", err)
		}
		a.Metadata = decodeMetadata(metaJSON)
		if t, perr := time.Parse(time.RFC3339, createdStr); perr == nil {
			a.CreatedAt = t.UTC()
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("run: iterate artifacts: %w", err)
	}
	return out, nil
}
