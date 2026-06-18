package previewenv

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ProvisionInput 是「为一条 PR 部署分配/刷新预览环境」的入参(由 run 终态钩子据运行元数据填齐)。
// 所有字段由上层解析(PR 号/分支/服务器/容器/宿主机 IP),本包只据预览配置组域名 + 分配 + 落库。
type ProvisionInput struct {
	ProjectID         string
	PipelineID        string
	PRNumber          int
	Branch            string
	ServerID          string
	UpstreamContainer string // 本次成功部署的上游容器(预览反代指向它)
	UpstreamPort      int
	HostIP            string // 宿主机公网 IPv4(由上层据 server.Host 解析;A 记录指向它)
}

// subdomainLabelRe 校验子域名 label 段(去掉非法字符后用于组 pr-<n>-<proj>)。
var subdomainLabelRe = regexp.MustCompile(`[^a-z0-9-]+`)

// Provision 为一条 PR 部署分配或刷新预览环境(R4 E4.1 核心)。
//
// **优雅降级铁律**:本方法是 best-effort —— 任何前置不满足(项目未开启预览 / 未配 DNS 提供商 /
// allocator 未装配 / 入参不全)都静默 no-op 返回 (nil, nil);任何下游失败都返回错误**供调用方记日志**,
// 但调用方(run 终态钩子)绝不能因此影响部署。本方法自身不 panic(纯逻辑);钩子侧再包一层 recover。
//
// 幂等:同一 (projectID, prNumber) 已有环境 → 更新该行(刷新路由/子域名),不重复建。
func (s *service) Provision(ctx context.Context, in ProvisionInput) (*PreviewEnv, error) {
	in.ProjectID = strings.TrimSpace(in.ProjectID)
	in.UpstreamContainer = strings.TrimSpace(in.UpstreamContainer)
	if in.ProjectID == "" || in.PRNumber < 1 {
		return nil, nil // 非 PR 运行 / 入参不全:静默不做。
	}
	if s.allocator == nil {
		return nil, nil // 分配器未装配(无 DNS 能力):优雅降级。
	}

	cfg, err := s.store.getConfig(ctx, in.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("previewenv: provision get config: %w", err)
	}
	if cfg == nil || !cfg.Enabled {
		return nil, nil // 项目未开启预览:no-op(绝不影响部署)。
	}
	if strings.TrimSpace(cfg.DNSProviderID) == "" || strings.TrimSpace(cfg.BaseDomain) == "" {
		return nil, nil // 配置不全:no-op。
	}
	if in.UpstreamContainer == "" || in.UpstreamPort < 1 || in.UpstreamPort > 65535 || strings.TrimSpace(in.HostIP) == "" {
		return nil, nil // 部署元数据不全:无从分配,静默。
	}

	subdomain := previewSubdomain(in.PRNumber, in.ProjectID, cfg.BaseDomain)

	// 分配 DNS-01 反代路由(复用 R3:建 A 记录 + 建路由)。失败上抛供钩子记日志,绝不阻断部署。
	routeID, aerr := s.allocator.Allocate(ctx, AllocateInput{
		ProviderID:        cfg.DNSProviderID,
		ServerID:          in.ServerID,
		UpstreamContainer: in.UpstreamContainer,
		UpstreamPort:      in.UpstreamPort,
		HostIP:            in.HostIP,
		Subdomain:         subdomain,
	})
	if aerr != nil {
		return nil, fmt.Errorf("previewenv: provision allocate: %w", aerr)
	}

	// 幂等:已有同 PR 环境 → 更新(刷路由/子域名/状态回 active);否则新建。
	existing, gerr := s.store.getByPR(ctx, in.ProjectID, in.PRNumber)
	if gerr == nil && existing != nil {
		// 同 PR 重新部署:删旧路由(best-effort,避免孤儿)后刷新本行指向新路由。
		if s.routeDeleter != nil && existing.RouteID != "" && existing.RouteID != routeID {
			_ = s.routeDeleter.DeleteRoute(ctx, existing.RouteID)
		}
		if uerr := s.store.updateOnRedeploy(ctx, existing.ID, routeID, subdomain, in.Branch, in.ServerID, in.PipelineID); uerr != nil {
			return nil, uerr
		}
		return s.store.get(ctx, existing.ID)
	}

	env := &PreviewEnv{
		ID:         newEnvID(),
		ProjectID:  in.ProjectID,
		PipelineID: in.PipelineID,
		PRNumber:   in.PRNumber,
		Branch:     in.Branch,
		ServerID:   in.ServerID,
		RouteID:    routeID,
		Subdomain:  subdomain,
		Status:     StatusActive,
		CreatedAt:  time.Now().UTC(),
	}
	if ierr := s.store.insert(ctx, env); ierr != nil {
		// 并发重复部署撞唯一约束:回退到「更新已存在行」路径(幂等收敛)。
		if ierr == ErrUniqueViolation {
			if cur, cerr := s.store.getByPR(ctx, in.ProjectID, in.PRNumber); cerr == nil && cur != nil {
				if uerr := s.store.updateOnRedeploy(ctx, cur.ID, routeID, subdomain, in.Branch, in.ServerID, in.PipelineID); uerr != nil {
					return nil, uerr
				}
				return s.store.get(ctx, cur.ID)
			}
		}
		return nil, ierr
	}
	return env, nil
}

// previewSubdomain 据 PR 号 + 项目 id + 根域组预览子域名:pr-<n>-<projLabel>.<base>。
// projLabel 取项目 id 前 8 字符并清洗为合法 DNS label 字符(小写字母数字连字符),保证 FQDN 合法。
func previewSubdomain(prNumber int, projectID, baseDomain string) string {
	label := sanitizeLabel(projectID)
	if len(label) > 8 {
		label = label[:8]
	}
	if label == "" {
		label = "p"
	}
	return "pr-" + strconv.Itoa(prNumber) + "-" + label + "." + strings.ToLower(strings.TrimSpace(baseDomain))
}

// sanitizeLabel 把任意字符串清洗为合法 DNS label 片段(小写、仅 a-z0-9-、去首尾连字符)。
func sanitizeLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = subdomainLabelRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
