package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/i18n"
)

// 飞书自定义机器人投递。
//
// 与通用 webhook 渠道的关键区别(也是后者发不进飞书的根因):
//   - 报文形状:飞书要自家形状(本实现发 {"msg_type":"interactive","card":{...}} 交互卡片),
//     而非通用 webhook 的 {title,body,fields};发错形状飞书返回 HTTP 200 但 body 内
//     code=19002「params error」。
//   - 成功判定:飞书**即便参数错误也回 HTTP 200**,故绝不能只看 HTTP 状态码,必须解析返回
//     JSON 的 code 字段(0=成功,非 0=失败,人读 msg 透出)。
//
// 卡片形态(对标钉钉 ActionCard / 企微 markdown):彩色头部(成功绿/失败红/回滚橙/中性蓝)+
// 标题 + lark_md 正文 + 字段双列(项目/分支/提交/状态/耗时…)。颜色据运行结果(event/status)推断。
//
// 可选签名:机器人若开启「签名校验」,配置时填密钥(走 vault 加密)。投递按飞书规范算
// timestamp + sign 注入 body 顶层;未配密钥则不带签名(与机器人未开校验一致)。

// ─── 交互卡片报文结构(飞书 message card v1) ─────────────────────────────────────

// feishuCardBody 是飞书交互卡片消息体(开启签名时另带顶层 timestamp/sign)。
type feishuCardBody struct {
	Timestamp string     `json:"timestamp,omitempty"`
	Sign      string     `json:"sign,omitempty"`
	MsgType   string     `json:"msg_type"` // 恒 "interactive"
	Card      feishuCard `json:"card"`
}

// feishuCard 卡片本体:config + 彩色 header + elements。
type feishuCard struct {
	Config   feishuCardConfig `json:"config"`
	Header   feishuCardHeader `json:"header"`
	Elements []feishuCardElem `json:"elements"`
}

type feishuCardConfig struct {
	WideScreenMode bool `json:"wide_screen_mode"`
}

// feishuCardHeader 彩色头部:template 是飞书内置主题色(green/red/orange/blue…)。
type feishuCardHeader struct {
	Template string         `json:"template"`
	Title    feishuCardText `json:"title"`
}

// feishuCardElem 是卡片元素:div(text 正文 / fields 双列)、hr(分隔线)、
// note(底部小字)、action(按钮组)。按 tag 取用对应字段,其余 omitempty。
type feishuCardElem struct {
	Tag      string            `json:"tag"`                // div / hr / note / action
	Text     *feishuCardText   `json:"text,omitempty"`     // div 正文段(lark_md)
	Fields   []feishuCardField `json:"fields,omitempty"`   // div 字段双列(lark_md)
	Elements []feishuCardText  `json:"elements,omitempty"` // note 段的小字元素
	Actions  []feishuActionBtn `json:"actions,omitempty"`  // action 段的按钮
}

// feishuActionBtn 是 action 段里的跳转按钮(点击打开 url)。
type feishuActionBtn struct {
	Tag  string         `json:"tag"`            // 恒 "button"
	Text feishuCardText `json:"text"`           // plain_text 按钮文案
	URL  string         `json:"url,omitempty"`  // 点击跳转地址
	Type string         `json:"type,omitempty"` // primary / default / danger
}

// feishuCardField 双列字段格:is_short=true 即半宽,两两并排成双列。
type feishuCardField struct {
	IsShort bool           `json:"is_short"`
	Text    feishuCardText `json:"text"`
}

// feishuCardText 飞书卡片文本:tag=plain_text(标题)或 lark_md(正文/字段,支持简易 md)。
type feishuCardText struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

// feishuResp 是飞书机器人返回体(只取判定/人读所需字段)。
type feishuResp struct {
	Code          int    `json:"code"`
	Msg           string `json:"msg"`
	StatusCode    int    `json:"StatusCode"`
	StatusMessage string `json:"StatusMessage"`
}

// sendFeishu 经注入的 *http.Client POST 文本消息到飞书机器人 webhook。
//   - SSRF 收口:投递前再校验一次 URL(与保存时一致;CheckRedirect 每跳复校)。
//   - 超时:注入 client.Timeout 兜底 + 另套 8s context。
//   - 成功判定:HTTP 2xx **且** 返回 JSON code==0(飞书参数错也回 200,必须看 code)。
//   - sealed 非空:解密为签名密钥,按飞书规范算 timestamp+sign。
func (s *service) sendFeishu(ctx context.Context, ch *Channel, sealed []byte, payload Payload) error {
	target := ch.Config.URL
	if !validWebhookURL(target) {
		return fmt.Errorf("飞书 webhook 地址不被允许:仅 http/https,且不可指向云元数据/链路本地地址")
	}

	body := feishuCardBody{
		MsgType: "interactive",
		Card:    feishuCardFor(payload),
	}

	// 可选签名:机器人开启「签名校验」时,配置里存了密钥(密文)。
	if len(sealed) > 0 {
		secret, err := s.openSecret(sealed)
		if err != nil {
			return fmt.Errorf("飞书签名密钥不可用:保险库未配置或密文损坏")
		}
		if strings.TrimSpace(secret) != "" {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			sign, serr := feishuSign(ts, secret)
			if serr != nil {
				return fmt.Errorf("飞书签名计算失败")
			}
			body.Timestamp = ts
			body.Sign = sign
		}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("飞书载荷序列化失败")
	}

	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("飞书请求构造失败:地址可能非法")
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
		return fmt.Errorf("飞书接收端返回非 2xx 状态:HTTP %d", resp.StatusCode)
	}

	// 飞书参数错误也回 HTTP 200,必须解析 code 才能判定真成功(否则误报成功、群里收不到)。
	var fr feishuResp
	if err := json.Unmarshal(respBody, &fr); err != nil {
		// 无法解析返回体:无从确认投递结果,按失败处理(绝不乐观)。
		return fmt.Errorf("飞书返回体无法解析,投递结果未知")
	}
	if fr.Code != 0 || fr.StatusCode != 0 {
		msg := strings.TrimSpace(fr.Msg)
		if msg == "" {
			msg = strings.TrimSpace(fr.StatusMessage)
		}
		code := fr.Code
		if code == 0 {
			code = fr.StatusCode
		}
		if msg == "" {
			return fmt.Errorf("飞书机器人拒收:code=%d", code)
		}
		return fmt.Errorf("飞书机器人拒收:code=%d %s", code, msg)
	}
	return nil
}

// feishuFieldOrder 字段在卡片里的展示顺序(业务可读优先,而非字母序);未列出的 key 追加在后、按字母序。
// actionUrl 不在此列:它单独渲染成底部行动按钮(见 feishuCardFor)。
var feishuFieldOrder = []string{"project", "branch", "commit", "status", "duration", "event"}

// feishuFieldLabel 字段 key → 中文标签(卡片里展示更友好)。未知 key 原样用 key。
func feishuFieldLabel(key, lang string) string {
	switch key {
	case "project":
		return i18n.T(lang, "项目")
	case "branch":
		return i18n.T(lang, "分支")
	case "commit":
		return i18n.T(lang, "提交")
	case "status":
		return i18n.T(lang, "状态")
	case "duration":
		return i18n.T(lang, "耗时")
	case "event":
		return i18n.T(lang, "事件")
	case "source":
		return i18n.T(lang, "来源")
	case "kind":
		return i18n.T(lang, "类型")
	case fieldActionURL:
		return i18n.T(lang, "审批链接")
	default:
		return key
	}
}

// feishuCardFor 把通知载荷拼成飞书交互卡片:状态图标 + 彩色头部 + lark_md 正文 +
// 分隔线 + 字段双列 + 行动按钮(有 actionUrl 时)+ 底部品牌小字。
func feishuCardFor(p Payload) feishuCard {
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = i18n.T(p.Lang, "Pipewright 通知")
	}
	// 头部标题前缀状态图标(✅/❌/🔄/🔔/🚀),让群里一眼看出结果。仅作用于飞书卡片。
	title = feishuTitleIcon(p) + " " + title

	card := feishuCard{
		Config: feishuCardConfig{WideScreenMode: true},
		Header: feishuCardHeader{
			Template: feishuHeaderColor(p),
			Title:    feishuCardText{Tag: "plain_text", Content: title},
		},
	}

	// 正文段(lark_md)。空则省略。
	if body := strings.TrimSpace(p.Body); body != "" {
		card.Elements = append(card.Elements, feishuCardElem{
			Tag:  "div",
			Text: &feishuCardText{Tag: "lark_md", Content: body},
		})
	}

	// 字段双列(lark_md,每格 **标签**\n值)。前置分隔线把正文与字段分区。
	if fields := feishuOrderedFields(p.Fields, p.Lang); len(fields) > 0 {
		if len(card.Elements) > 0 {
			card.Elements = append(card.Elements, feishuCardElem{Tag: "hr"})
		}
		card.Elements = append(card.Elements, feishuCardElem{Tag: "div", Fields: fields})
	}

	// 行动按钮:有 actionUrl 时渲染成可点击按钮(审批用主色 primary,其余用默认色)。
	if url := strings.TrimSpace(p.Fields[fieldActionURL]); url != "" {
		label := i18n.T(p.Lang, "查看运行详情")
		btnType := "default"
		if strings.Contains(strings.ToLower(p.Fields["event"]), "approval") {
			label = i18n.T(p.Lang, "前往审批")
			btnType = "primary"
		}
		card.Elements = append(card.Elements, feishuCardElem{
			Tag: "action",
			Actions: []feishuActionBtn{{
				Tag:  "button",
				Text: feishuCardText{Tag: "plain_text", Content: label},
				URL:  url,
				Type: btnType,
			}},
		})
	}

	// 底部品牌小字(note 段)。始终存在,故 elements 永不为空,无需占位兜底。
	if len(card.Elements) > 0 {
		card.Elements = append(card.Elements, feishuCardElem{Tag: "hr"})
	}
	card.Elements = append(card.Elements, feishuCardElem{
		Tag:      "note",
		Elements: []feishuCardText{{Tag: "lark_md", Content: i18n.T(p.Lang, "🤖 来自 Pipewright · 自动化 CI/CD")}},
	})
	return card
}

// feishuTitleIcon 据运行结果给标题选个状态图标(与 feishuHeaderColor 同源语义)。
func feishuTitleIcon(p Payload) string {
	event := strings.ToLower(strings.TrimSpace(p.Fields["event"]))
	status := strings.ToLower(strings.TrimSpace(p.Fields["status"]))
	switch {
	case strings.Contains(event, "approval"):
		return "🔔"
	case strings.Contains(event, "rollback") || strings.Contains(status, "rolled_back") || strings.Contains(status, "rollback"):
		return "🔄"
	case strings.Contains(event, "fail") || strings.Contains(status, "fail"):
		return "❌"
	case strings.Contains(event, "succeed") || status == "success" || strings.Contains(status, "success"):
		return "✅"
	default:
		return "🚀"
	}
}

// feishuOrderedFields 把 Payload.Fields 渲染成双列卡片字段格(业务顺序优先,稳定)。
func feishuOrderedFields(fields map[string]string, lang string) []feishuCardField {
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(fields))
	keys := make([]string, 0, len(fields))
	// 1. 先按预定义业务顺序
	for _, k := range feishuFieldOrder {
		if _, ok := fields[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	// 2. 其余 key 按字母序追加(稳定,避免 map 迭代乱序);actionUrl 单独走按钮,跳过。
	rest := make([]string, 0)
	for k := range fields {
		if !seen[k] && k != fieldActionURL {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)

	out := make([]feishuCardField, 0, len(keys))
	for _, k := range keys {
		value := fields[k]
		// 状态字段值前置图标,双列里也一眼可读。
		if k == "status" {
			value = feishuStatusIcon(value) + " " + value
		}
		out = append(out, feishuCardField{
			IsShort: true, // 半宽 → 两两并排成双列
			Text: feishuCardText{
				Tag:     "lark_md",
				Content: "**" + feishuFieldLabel(k, lang) + "**\n" + value,
			},
		})
	}
	return out
}

// feishuStatusIcon 据 status 文本选图标(用于字段双列里的状态值)。
func feishuStatusIcon(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.Contains(s, "rolled_back") || strings.Contains(s, "rollback"):
		return "🔄"
	case strings.Contains(s, "fail"):
		return "❌"
	case strings.Contains(s, "success"):
		return "✅"
	case strings.Contains(s, "running") || strings.Contains(s, "progress"):
		return "⏳"
	default:
		return "•"
	}
}

// feishuHeaderColor 据运行结果推断卡片头部主题色(飞书内置 template):
// 成功绿 / 失败红 / 回滚橙 / 其余中性蓝。优先看 event(语义最准),回退 status。
func feishuHeaderColor(p Payload) string {
	event := strings.ToLower(strings.TrimSpace(p.Fields["event"]))
	status := strings.ToLower(strings.TrimSpace(p.Fields["status"]))

	// 回滚单独橙色(既非成功也非纯失败)。
	if strings.Contains(event, "rollback") || strings.Contains(status, "rolled_back") || strings.Contains(status, "rollback") {
		return "orange"
	}
	// 失败:event 含 failed,或 status 含 fail(failed / partial_failed)。
	if strings.Contains(event, "fail") || strings.Contains(status, "fail") {
		return "red"
	}
	// 成功:event 含 succeeded,或 status==success。
	if strings.Contains(event, "succeed") || status == "success" || strings.Contains(status, "success") {
		return "green"
	}
	return "blue"
}

// feishuSign 按飞书规范算签名:stringToSign = "{timestamp}\n{secret}",
// sign = base64(HMAC-SHA256(key=stringToSign, message=empty))。
func feishuSign(timestamp, secret string) (string, error) {
	stringToSign := timestamp + "\n" + secret
	h := hmac.New(sha256.New, []byte(stringToSign))
	if _, err := h.Write([]byte{}); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}
