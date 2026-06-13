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
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/i18n"
)

// 钉钉自定义群机器人投递。
//
// 配置 = 群机器人 webhook 地址(复用 ChannelConfig.URL)+ 可选加签密钥(走 vault 加密,
// 与 email SMTP 密码同一套 write-only 密文模式)。报文形状为钉钉 markdown:
//
//	{"msgtype":"markdown","markdown":{"title":"<标题>","text":"<markdown 正文>"}}
//
// 可选加签:机器人「安全设置」选「加签」时,配置里存密钥。投递按钉钉规范:
//   - timestamp = 当前毫秒时间戳(字符串)
//   - stringToSign = "{timestamp}\n{secret}"
//   - sign = urlEncode( base64( HMAC-SHA256(key=secret, message=stringToSign) ) )
//   - 把 &timestamp=...&sign=... 附到 webhook URL query 上。
//
// 成功判定:钉钉即便参数/签名错误也常回 HTTP 200,但 body 内 errcode!=0,必须解析判定。
// SSRF 收口与通用 webhook 一致(投递前再校验一次 URL,CheckRedirect 每跳复校)。

// dingtalkMarkdownBody 是钉钉群机器人 markdown 消息体。
type dingtalkMarkdownBody struct {
	MsgType  string              `json:"msgtype"` // 恒 "markdown"
	Markdown dingtalkMarkdownObj `json:"markdown"`
}

type dingtalkMarkdownObj struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// dingtalkResp 是钉钉机器人返回体(只取判定/人读所需字段)。
type dingtalkResp struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// sendDingtalk 经注入的 *http.Client POST markdown 消息到钉钉群机器人 webhook。
//   - SSRF 收口:投递前再校验一次 URL(与保存时一致;CheckRedirect 每跳复校)。
//   - 超时:注入 client.Timeout 兜底 + 另套 8s context。
//   - 成功判定:HTTP 2xx **且** 返回 JSON errcode==0(钉钉参数错也回 200,必须看 errcode)。
//   - sealed 非空:解密为加签密钥,按钉钉规范算 timestamp+sign 附到 URL query。
func (s *service) sendDingtalk(ctx context.Context, ch *Channel, sealed []byte, payload Payload) error {
	target := ch.Config.URL
	if !validWebhookURL(target) {
		return fmt.Errorf("钉钉 webhook 地址不被允许:仅 http/https,且不可指向云元数据/链路本地地址")
	}

	// 可选加签:机器人「安全设置」选「加签」时,配置里存了密钥(密文)。
	if len(sealed) > 0 {
		secret, err := s.openSecret(sealed)
		if err != nil {
			return fmt.Errorf("钉钉加签密钥不可用:保险库未配置或密文损坏")
		}
		if strings.TrimSpace(secret) != "" {
			ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
			sign := dingtalkSign(ts, secret)
			signed, serr := appendDingtalkSign(target, ts, sign)
			if serr != nil {
				return fmt.Errorf("钉钉 webhook 地址非法,无法附加签名")
			}
			target = signed
		}
	}

	title := strings.TrimSpace(payload.Title)
	if title == "" {
		title = i18n.T(payload.Lang, "Pipewright 通知")
	}
	body := dingtalkMarkdownBody{
		MsgType: "markdown",
		Markdown: dingtalkMarkdownObj{
			Title: title,
			Text:  renderMarkdownBody(payload),
		},
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("钉钉载荷序列化失败")
	}

	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("钉钉请求构造失败:地址可能非法")
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
		return fmt.Errorf("钉钉接收端返回非 2xx 状态:HTTP %d", resp.StatusCode)
	}

	// 钉钉参数/签名错误也回 HTTP 200,必须解析 errcode 才能判定真成功(否则误报成功、群里收不到)。
	var dr dingtalkResp
	if err := json.Unmarshal(respBody, &dr); err != nil {
		return fmt.Errorf("钉钉返回体无法解析,投递结果未知")
	}
	if dr.ErrCode != 0 {
		msg := strings.TrimSpace(dr.ErrMsg)
		if msg == "" {
			return fmt.Errorf("钉钉机器人拒收:errcode=%d", dr.ErrCode)
		}
		return fmt.Errorf("钉钉机器人拒收:errcode=%d %s", dr.ErrCode, msg)
	}
	return nil
}

// dingtalkSign 按钉钉规范算加签:stringToSign = "{timestamp}\n{secret}",
// sign = base64( HMAC-SHA256(key=secret, message=stringToSign) )。
// 注意 key 是密钥本身、message 是 stringToSign(与飞书的 key=stringToSign 相反)。
// 返回的是尚未 URL 编码的 base64;附到 URL query 时由 url.Values 编码。
func dingtalkSign(timestamp, secret string) string {
	stringToSign := timestamp + "\n" + secret
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// appendDingtalkSign 把 timestamp + sign 作为 query 参数附到 webhook URL 上(sign 经 URL 编码)。
func appendDingtalkSign(raw, timestamp, sign string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("timestamp", timestamp)
	q.Set("sign", sign)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
