package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/mask"
)

// GenerateCompose:中文需求 → docker-compose.yml(容器管理「AI 生成 compose」)。
// 与 CommandSuggest 一致——生成本就需要 AI:未配置/未启用 → ErrAINotConfigured。

const composeGenMaxTokens = 1500

// GenerateComposeInput 是生成 compose 的入参。
type GenerateComposeInput struct {
	NL     string       // 中文需求(如「nginx + redis,nginx 映射 8080,数据持久化」)
	Masker *mask.Masker // 出网前脱敏(NL 经 Scrub)
}

// ComposeResult 是生成结果。
type ComposeResult struct {
	YAML        string    `json:"yaml"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// GenerateCompose 据中文需求生成一份 docker-compose.yml(纯 yaml,无围栏/解释)。
func (s *service) GenerateCompose(ctx context.Context, in GenerateComposeInput) (*ComposeResult, error) {
	masker := in.Masker
	if masker == nil {
		masker = mask.NewMasker()
	}
	nl := strings.TrimSpace(in.NL)
	if nl == "" {
		return nil, fmt.Errorf("%w: 请描述你想要的 compose", ErrGenerateFailed)
	}

	provider, baseURL, apiKey, err := s.resolveChatCreds(ctx)
	if err != nil {
		return nil, err
	}

	prompt := buildComposePrompt(masker, nl)
	text, cerr := s.chatWithTokens(ctx, provider, baseURL, s.modelFor(ctx), apiKey, prompt, composeGenMaxTokens)
	apiKey = "" // 明文用完即弃
	_ = apiKey
	if cerr != nil {
		return nil, cerr
	}

	yaml := masker.Scrub(stripCodeFence(strings.TrimSpace(text)))
	if yaml == "" {
		return nil, fmt.Errorf("%w: 模型未产出 compose", ErrGenerateFailed)
	}
	return &ComposeResult{YAML: yaml, GeneratedAt: time.Now().UTC()}, nil
}

func buildComposePrompt(m *mask.Masker, nl string) string {
	var b strings.Builder
	b.WriteString("你是 Docker / Compose 专家。根据用户的中文需求,产出一份可直接用 `docker compose up -d` 部署的 docker-compose.yml。\n\n")
	b.WriteString("## 用户需求\n")
	b.WriteString(truncateRunes(m.Scrub(nl), maxNLChars))
	b.WriteString("\n\n")
	b.WriteString(`## 输出要求
- **只输出 compose 文件本身的 YAML 内容**,不要任何解释文字、不要 markdown 代码块围栏(不要 ` + "```" + `)。
- 使用 Compose 规范(顶层 services:,可含 volumes:/networks:);不写已废弃的 version 字段。
- 镜像用明确 tag;端口、环境变量、卷挂载按需求给全;合理设置 restart 策略。
- 绝不写入明文密钥 / token / 密码,需要凭据处用占位符或环境变量引用。
`)
	return b.String()
}
