package trigger

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// EnvironmentResolver 把(项目, 分支)解析为映射的环境名 + 目标服务器引用 id 列表,
// 复用与 webhook 接收路径**完全相同**的分支映射 + glob 匹配(matchBranch)。
//
// 用途(#56):手动 / 定时 / 串联触发经 run.Service.Create 创建运行时不走 webhook 接收器,
// 此前恒不解析环境(ResolvedEnvironment 空)→ build_image 不知推哪个 registry、环境变量 /
// 目标服务器全落空。注入本解析器后,这些触发也按项目分支映射补解析,与 webhook 一致。
//
// 只读 branch_mappings_json(不碰 webhook 签名密钥 / master key);实现 run.EnvResolver。
type EnvironmentResolver struct {
	db *sql.DB
}

// NewEnvironmentResolver 构造分支→环境解析器(db 经参数化 SQL 触库)。
func NewEnvironmentResolver(db *sql.DB) *EnvironmentResolver {
	return &EnvironmentResolver{db: db}
}

// ResolveEnv 返回 branch 在项目分支映射中首条命中的环境名 + 目标服务器 id 列表。
// 无配置 / 空分支 / 无命中 / 解析失败 → ("", nil)(调用方据此保持「未解析」语义)。
func (r *EnvironmentResolver) ResolveEnv(ctx context.Context, projectID, branch string) (string, []string) {
	branch = strings.TrimSpace(branch)
	projectID = strings.TrimSpace(projectID)
	if r == nil || r.db == nil || branch == "" || projectID == "" {
		return "", nil
	}

	var mappingsJSON string
	err := r.db.QueryRowContext(ctx,
		`SELECT branch_mappings_json FROM pipeline_triggers WHERE project_id = ?`, projectID,
	).Scan(&mappingsJSON)
	if err != nil || strings.TrimSpace(mappingsJSON) == "" {
		// 无行(项目无触发配置)/ 空映射 / 查询错误 → 不解析(best-effort,绝不让创建运行失败)。
		return "", nil
	}

	var mappings []BranchMapping
	if jerr := json.Unmarshal([]byte(mappingsJSON), &mappings); jerr != nil {
		return "", nil
	}
	m, ok := matchBranch(branch, mappings)
	if !ok {
		return "", nil
	}
	return m.Environment, m.TargetServerIDs
}
