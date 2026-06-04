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
)

// 飞书自定义机器人投递。
//
// 与通用 webhook 渠道的关键区别(也是后者发不进飞书的根因):
//   - 报文形状:飞书要 {"msg_type":"text","content":{"text":...}},而非通用 webhook 的
//     {title,body,fields};发错形状飞书返回 HTTP 200 但 body 内 code=19002「params error」。
//   - 成功判定:飞书**即便参数错误也回 HTTP 200**,故绝不能只看 HTTP 状态码,必须解析返回
//     JSON 的 code 字段(0=成功,非 0=失败,人读 msg 透出)。
//
// 可选签名:机器人若开启「签名校验」,配置时填密钥(走 vault 加密)。投递按飞书规范算
// timestamp + sign 注入 body;未配密钥则不带签名(与机器人未开校验一致)。

// feishuTextBody 是飞书文本消息体(开启签名时另带 timestamp/sign)。
type feishuTextBody struct {
	Timestamp string `json:"timestamp,omitempty"`
	Sign      string `json:"sign,omitempty"`
	MsgType   string `json:"msg_type"`
	Content   struct {
		Text string `json:"text"`
	} `json:"content"`
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

	var body feishuTextBody
	body.MsgType = "text"
	body.Content.Text = feishuText(payload)

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

// feishuText 把通知载荷拼成飞书文本消息正文(标题 + 正文 + 结构化字段)。
func feishuText(p Payload) string {
	var b strings.Builder
	if t := strings.TrimSpace(p.Title); t != "" {
		b.WriteString(t)
	}
	if body := strings.TrimSpace(p.Body); body != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(body)
	}
	if len(p.Fields) > 0 {
		keys := make([]string, 0, len(p.Fields))
		for k := range p.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys) // 稳定顺序(map 迭代无序,避免投递文案抖动)
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		for _, k := range keys {
			b.WriteString("\n")
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(p.Fields[k])
		}
	}
	if b.Len() == 0 {
		return "(空通知)" // 飞书 text 不可为空
	}
	return b.String()
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
