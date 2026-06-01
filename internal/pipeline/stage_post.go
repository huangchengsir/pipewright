package pipeline

import (
	"fmt"
	"strings"
)

// stage_post.go 定义阶段「后置步骤」(post,P1 · 对标 Jenkins post / GitLab after_script)的模型与校验。
//
// 阶段的 job 跑完后,post 步骤**无论阶段成功失败都按 condition 执行**(在同一克隆工作区,可访问构建
// 产物),用于清理 / 通知 / 归档。执行见 internal/build/dag_stage_exec.go 的 runStagePost。

// post 步骤条件枚举。
const (
	// PostAlways:无论成功失败都跑(清理类)。
	PostAlways = "always"
	// PostOnSuccess:仅阶段成功跑。
	PostOnSuccess = "on_success"
	// PostOnFailure:仅阶段失败(含取消)跑。
	PostOnFailure = "on_failure"
)

// maxPostSteps 是单阶段后置步骤条数上界(防误填巨量)。
const maxPostSteps = 32

// PostStep 是一个阶段后置步骤(脚本类:镜像 + 多行命令 + 可选工作目录 + 触发条件)。
type PostStep struct {
	// Condition 为 always | on_success | on_failure(空 → always)。
	Condition string `json:"condition"`
	// Image 是运行镜像(必填)。
	Image string `json:"image"`
	// Commands 是多行命令(顺序执行,至少一条非空)。
	Commands []string `json:"commands"`
	// WorkDir 是容器内相对工作目录(相对克隆工作区根;空 = 根)。
	WorkDir string `json:"workDir,omitempty"`
}

// PostConditionMatches 报告某 condition 在「阶段是否失败」下是否应执行。
func PostConditionMatches(condition string, stageFailed bool) bool {
	switch normalizePostCondition(condition) {
	case PostOnSuccess:
		return !stageFailed
	case PostOnFailure:
		return stageFailed
	default: // always
		return true
	}
}

func normalizePostCondition(c string) string {
	switch strings.TrimSpace(strings.ToLower(c)) {
	case PostOnSuccess:
		return PostOnSuccess
	case PostOnFailure:
		return PostOnFailure
	case PostAlways, "":
		return PostAlways
	default:
		return "" // 非法标记,由 normalizePost 校验报错
	}
}

// normalizePost 规范化 + 校验阶段后置步骤:condition 合法、image 非空、至少一条非空命令。
// 空输入 → nil(行为不变)。
func normalizePost(in []PostStep) ([]PostStep, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxPostSteps {
		return nil, fmt.Errorf("%w: post 步骤数 %d 超过上限 %d", ErrInvalidStage, len(in), maxPostSteps)
	}
	out := make([]PostStep, 0, len(in))
	for i, ps := range in {
		cond := strings.TrimSpace(strings.ToLower(ps.Condition))
		if cond == "" {
			cond = PostAlways
		}
		if cond != PostAlways && cond != PostOnSuccess && cond != PostOnFailure {
			return nil, fmt.Errorf("%w: post 步骤 #%d 的 condition %q 非法(须为 always/on_success/on_failure)", ErrInvalidStage, i+1, ps.Condition)
		}
		image := strings.TrimSpace(ps.Image)
		if image == "" {
			return nil, fmt.Errorf("%w: post 步骤 #%d 缺少镜像(image)", ErrInvalidStage, i+1)
		}
		cmds := make([]string, 0, len(ps.Commands))
		hasCmd := false
		for _, c := range ps.Commands {
			cmds = append(cmds, c)
			if strings.TrimSpace(c) != "" {
				hasCmd = true
			}
		}
		if !hasCmd {
			return nil, fmt.Errorf("%w: post 步骤 #%d 至少需要一条非空命令", ErrInvalidStage, i+1)
		}
		out = append(out, PostStep{Condition: cond, Image: image, Commands: cmds, WorkDir: strings.TrimSpace(ps.WorkDir)})
	}
	return out, nil
}
