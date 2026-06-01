package pipeline

import (
	"fmt"
	"regexp"
	"strings"
)

// stage_services.go 定义阶段「旁挂服务」(services,P1 · 对标 GitLab services / Woodpecker services)
// 的模型与校验。服务容器(DB/redis 等)与该阶段脚本容器同 docker 网络、按服务名(network-alias)
// 互访(如脚本里 `psql -h testdb`)。执行见 internal/build/dag_stage_exec.go 的 runStageServices。

// maxStageServices 是单阶段旁挂服务数上界。
const maxStageServices = 16

// 服务名:docker network-alias / 容器名安全(字母/下划线起,后接字母数字下划线连字符)。
var serviceNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// ServiceSpec 是一个旁挂服务(镜像 + 服务名 + 可选环境变量 + 可选端口映射)。
type ServiceSpec struct {
	// Name 是服务名:同网络内的访问主机名(network-alias)+ 容器名后缀。必填、阶段内唯一。
	Name string `json:"name"`
	// Image 是服务镜像(如 postgres:16 / redis:7)。必填。
	Image string `json:"image"`
	// Env 是服务容器环境变量(K=V,如 POSTGRES_PASSWORD=x)。可选。
	Env []string `json:"env,omitempty"`
	// Ports 是端口映射(host:container,可选;同网络互访通常无需暴露端口)。
	Ports []string `json:"ports,omitempty"`
}

// normalizeServices 规范化 + 校验阶段旁挂服务:服务名合法/唯一、image 非空。空 → nil(行为不变)。
func normalizeServices(in []ServiceSpec) ([]ServiceSpec, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxStageServices {
		return nil, fmt.Errorf("%w: 旁挂服务数 %d 超过上限 %d", ErrInvalidStage, len(in), maxStageServices)
	}
	out := make([]ServiceSpec, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for i, sv := range in {
		name := strings.TrimSpace(sv.Name)
		if !serviceNameRe.MatchString(name) {
			return nil, fmt.Errorf("%w: 服务 #%d 名称 %q 非法(须字母/下划线起,仅含字母数字下划线连字符)", ErrInvalidStage, i+1, sv.Name)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("%w: 服务名 %q 重复", ErrInvalidStage, name)
		}
		seen[name] = struct{}{}
		image := strings.TrimSpace(sv.Image)
		if image == "" {
			return nil, fmt.Errorf("%w: 服务 %q 缺少镜像(image)", ErrInvalidStage, name)
		}
		out = append(out, ServiceSpec{Name: name, Image: image, Env: trimNonEmpty(sv.Env), Ports: trimNonEmpty(sv.Ports)})
	}
	return out, nil
}

func trimNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
