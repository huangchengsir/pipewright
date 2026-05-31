// Package promotion 实现环境晋级流(Epic 8 · Story 8-7 / FR-8-7):把一次成功运行/产物
// 沿一条**有序环境链**(dev → staging → prod)逐级晋级,逐环境可设审批门,逐环境作用域
// 隔离变量/密钥。
//
// 设计要点(诚实):
//   - 环境链是「每项目一条有序数组」配置(如 ["dev","staging","prod"]),每环境可标 gated。
//   - 晋级状态机:只能晋级到「链上当前最高已达环境的下一级」;不可跳级、不可越过链尾(prod)。
//     首次晋级目标须为链首(index 0)。
//   - gated 目标环境:复用 FR-8-4 的审批门内核(approval.Coordinator + run_approvals),
//     登记一条待批、阻塞等待批准/拒绝;批准 → 落 promoted,拒绝/超时/取消 → rejected。
//   - 逐环境变量/密钥:env-scoped key=value;secret 变量只存 credential_id 引用保险库(无明文)。
//     晋级到某环境时注入该环境作用域变量(consistent 与 build 的参数注入)。
//
// 本包**不** import dagrun(晋级编排是 run 执行之上的高层概念,经最小 hook 解耦);
// 也不复制审批逻辑(经注入的 Gate 复用 approval 内核)。
package promotion

import (
	"errors"
	"strings"
)

// 晋级记录状态枚举(DB 存小写串)。
const (
	// StatusPending 表示晋级待审批(gated 环境,阻塞在审批门)。
	StatusPending = "pending"
	// StatusPromoted 表示已成功晋级(终态)。
	StatusPromoted = "promoted"
	// StatusRejected 表示晋级被拒/超时/取消(终态)。
	StatusRejected = "rejected"
)

// 领域错误。错误体绝无敏感数据(密钥/明文)。
var (
	// ErrChainNotConfigured 表示项目尚未配置环境链。
	ErrChainNotConfigured = errors.New("promotion: environment chain not configured")
	// ErrEmptyChain 表示配置的环境链为空。
	ErrEmptyChain = errors.New("promotion: environment chain must not be empty")
	// ErrDuplicateEnv 表示环境链含重复环境名。
	ErrDuplicateEnv = errors.New("promotion: duplicate environment in chain")
	// ErrInvalidEnvName 表示环境名为空或非法。
	ErrInvalidEnvName = errors.New("promotion: invalid environment name")
	// ErrUnknownEnv 表示目标环境不在链上。
	ErrUnknownEnv = errors.New("promotion: target environment not in chain")
	// ErrRunNotSuccessful 表示源运行未成功,不可晋级。
	ErrRunNotSuccessful = errors.New("promotion: source run is not successful")
	// ErrSkipEnv 表示试图跳级(只能晋级到下一级)。
	ErrSkipEnv = errors.New("promotion: cannot skip environments; promote to the next environment in order")
	// ErrAlreadyAtTop 表示已在链尾,无更高环境可晋级。
	ErrAlreadyAtTop = errors.New("promotion: run already promoted to the top environment")
	// ErrAlreadyPromoted 表示该运行已晋级到该环境(幂等拒绝重复)。
	ErrAlreadyPromoted = errors.New("promotion: run already promoted to this environment")
	// ErrRunNotFound 表示源运行不存在。
	ErrRunNotFound = errors.New("promotion: source run not found")
	// ErrProjectNotFound 表示项目不存在。
	ErrProjectNotFound = errors.New("promotion: project not found")
	// ErrGateRejected 表示 gated 环境审批门被拒绝/超时/取消。
	ErrGateRejected = errors.New("promotion: approval gate rejected")
	// ErrVarKeyEmpty 表示变量 key 为空。
	ErrVarKeyEmpty = errors.New("promotion: variable key must not be empty")
	// ErrVarKeyDuplicate 表示同环境内变量 key 重复。
	ErrVarKeyDuplicate = errors.New("promotion: duplicate variable key in environment")
)

// EnvStage 是环境链上的一级:环境名 + 是否需要审批门。
type EnvStage struct {
	Name  string `json:"name"`
	Gated bool   `json:"gated"`
}

// Chain 是某项目的有序环境链(index 0 为链首,如 dev)。
type Chain struct {
	Environments []EnvStage `json:"environments"`
}

// IndexOf 返回环境在链上的下标;不在链上 → -1。
func (c Chain) IndexOf(env string) int {
	for i := range c.Environments {
		if c.Environments[i].Name == env {
			return i
		}
	}
	return -1
}

// Gated 报告某环境是否需要审批门(不在链上视为非 gated)。
func (c Chain) Gated(env string) bool {
	i := c.IndexOf(env)
	if i < 0 {
		return false
	}
	return c.Environments[i].Gated
}

// Names 返回链上环境名(有序)。
func (c Chain) Names() []string {
	out := make([]string, len(c.Environments))
	for i := range c.Environments {
		out[i] = c.Environments[i].Name
	}
	return out
}

// validateChain 校验链:非空、无空名、无重名。返回规整后的链(去首尾空白)。
func validateChain(c Chain) (Chain, error) {
	if len(c.Environments) == 0 {
		return Chain{}, ErrEmptyChain
	}
	seen := map[string]struct{}{}
	out := Chain{Environments: make([]EnvStage, 0, len(c.Environments))}
	for _, e := range c.Environments {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			return Chain{}, ErrInvalidEnvName
		}
		if _, dup := seen[name]; dup {
			return Chain{}, ErrDuplicateEnv
		}
		seen[name] = struct{}{}
		out.Environments = append(out.Environments, EnvStage{Name: name, Gated: e.Gated})
	}
	return out, nil
}

// Record 是一条晋级记录(供 UI / 审计)。
type Record struct {
	ID                string `json:"id"`
	ProjectID         string `json:"projectId"`
	SourceRunID       string `json:"sourceRunId"`
	FromEnvironment   string `json:"fromEnvironment"`
	TargetEnvironment string `json:"targetEnvironment"`
	Status            string `json:"status"`
	ApprovalStage     string `json:"approvalStage"`
	PromotedBy        string `json:"promotedBy"`
	CreatedAt         string `json:"createdAt"`
	DecidedAt         string `json:"decidedAt"`
}

// Variable 是一条环境作用域变量(对外视图)。secret 变量只暴露 credentialId,绝无明文密钥。
type Variable struct {
	Key          string `json:"key"`
	Value        string `json:"value"` // 仅非 secret 有值;secret 恒为空
	Secret       bool   `json:"secret"`
	CredentialID string `json:"credentialId"` // 仅 secret 有值
}

// ResolvedVar 是注入执行环境的一条解析后变量(明文 K=V)。
// secret 变量在此已由保险库解密为明文,**仅供进程内注入**,绝不回 HTTP。
type ResolvedVar struct {
	Key    string
	Value  string
	Secret bool
}

// nextTarget 给定链与「源运行当前已达环境」,算出下一个可晋级到的目标环境。
// curEnv 为空表示尚未晋级过(首次晋级目标 = 链首)。
func (c Chain) nextTarget(curEnv string) (target string, err error) {
	if len(c.Environments) == 0 {
		return "", ErrEmptyChain
	}
	if strings.TrimSpace(curEnv) == "" {
		return c.Environments[0].Name, nil
	}
	i := c.IndexOf(curEnv)
	if i < 0 {
		return "", ErrUnknownEnv
	}
	if i >= len(c.Environments)-1 {
		return "", ErrAlreadyAtTop
	}
	return c.Environments[i+1].Name, nil
}

// validateTarget 校验把当前已达 curEnv 的运行晋级到 target 是否合法(不可跳级/越尾)。
func (c Chain) validateTarget(curEnv, target string) error {
	ti := c.IndexOf(target)
	if ti < 0 {
		return ErrUnknownEnv
	}
	want, err := c.nextTarget(curEnv)
	if err != nil {
		return err
	}
	if want != target {
		return ErrSkipEnv
	}
	return nil
}
