package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/i18n"
)

// 企业微信群机器人投递。
//
// 配置 = 群机器人 webhook 地址(复用 ChannelConfig.URL),群机器人本身不需签名密钥
// (鉴权靠 URL 内置的 key 参数)。报文形状为企微 markdown:
//
//	{"msgtype":"markdown","markdown":{"content":"<渲染后的 markdown 正文>"}}
//
// 成功判定:与飞书同理,企微即便参数错误也常回 HTTP 200,但 body 内 errcode!=0。
// 必须解析返回 JSON 的 errcode(0=成功,非 0=失败,人读 errmsg 透出),否则会误报成功、群里收不到。
// SSRF 收口与通用 webhook 一致(投递前再校验一次 URL,CheckRedirect 每跳复校)。

// wecomMarkdownBody 是企微群机器人 markdown 消息体。
type wecomMarkdownBody struct {
	MsgType  string           `json:"msgtype"` // 恒 "markdown"
	Markdown wecomMarkdownObj `json:"markdown"`
}

type wecomMarkdownObj struct {
	Content string `json:"content"`
}

// wecomResp 是企微机器人返回体(只取判定/人读所需字段)。
type wecomResp struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// sendWecom 经注入的 *http.Client POST markdown 消息到企微群机器人 webhook。
//   - SSRF 收口:投递前再校验一次 URL(与保存时一致;CheckRedirect 每跳复校)。
//   - 超时:注入 client.Timeout 兜底 + 另套 8s context。
//   - 成功判定:HTTP 2xx **且** 返回 JSON errcode==0(企微参数错也回 200,必须看 errcode)。
//   - 群机器人无需签名,故不取用 sealed(无敏感字段)。
func (s *service) sendWecom(ctx context.Context, ch *Channel, payload Payload) error {
	target := ch.Config.URL
	if !validWebhookURL(target) {
		return fmt.Errorf("企业微信 webhook 地址不被允许:仅 http/https,且不可指向云元数据/链路本地地址")
	}

	body := wecomMarkdownBody{
		MsgType:  "markdown",
		Markdown: wecomMarkdownObj{Content: renderMarkdownBody(payload)},
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("企业微信载荷序列化失败")
	}

	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("企业微信请求构造失败:地址可能非法")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Pipewright-notify/1")

	resp, err := s.client.Do(req)
	if err != nil {
		return mapWebhookTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxWebhookRespBytes))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("企业微信接收端返回非 2xx 状态:HTTP %d", resp.StatusCode)
	}

	// 企微参数错误也回 HTTP 200,必须解析 errcode 才能判定真成功(否则误报成功、群里收不到)。
	var wr wecomResp
	if err := json.Unmarshal(respBody, &wr); err != nil {
		return fmt.Errorf("企业微信返回体无法解析,投递结果未知")
	}
	if wr.ErrCode != 0 {
		msg := strings.TrimSpace(wr.ErrMsg)
		if msg == "" {
			return fmt.Errorf("企业微信机器人拒收:errcode=%d", wr.ErrCode)
		}
		return fmt.Errorf("企业微信机器人拒收:errcode=%d %s", wr.ErrCode, msg)
	}
	return nil
}

// ─── 通用 markdown 正文渲染(企微 / 钉钉共用) ───────────────────────────────────
//
// 把 Payload(标题 + 正文 + 结构化字段)拼成一段 markdown:
//   - 标题:一级标题 "# {Title}"。
//   - 正文:原样一段。
//   - 字段:按业务顺序(复用 feishuFieldOrder)逐行 "**标签**:值",中文/locale 标签复用 feishuFieldLabel。
//
// 钉钉/企微 markdown 语法基本一致,故共用一份渲染。标题另由各自的 title 字段透出。
func renderMarkdownBody(p Payload) string {
	var b strings.Builder
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = i18n.T(p.Lang, "Pipewright 通知")
	}
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n")

	if body := strings.TrimSpace(p.Body); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}

	if fields := orderedFieldKeys(p.Fields); len(fields) > 0 {
		b.WriteString("\n")
		for _, k := range fields {
			b.WriteString("**")
			b.WriteString(feishuFieldLabel(k, p.Lang))
			b.WriteString("**:")
			b.WriteString(p.Fields[k])
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// orderedFieldKeys 返回字段 key 的稳定展示顺序(业务顺序优先,复用 feishuFieldOrder;其余字母序)。
func orderedFieldKeys(fields map[string]string) []string {
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(fields))
	keys := make([]string, 0, len(fields))
	for _, k := range feishuFieldOrder {
		if _, ok := fields[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	rest := make([]string, 0)
	for k := range fields {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}
