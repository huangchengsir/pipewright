package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxWebhookRespBytes 限制 webhook 响应体读取量(防恶意/巨大响应撑爆内存)。
const maxWebhookRespBytes = 64 << 10 // 64KB

// webhookEnvelope 是 webhook POST 的 JSON 体(稳定形状,供接收端解析)。
type webhookEnvelope struct {
	Title  string            `json:"title"`
	Body   string            `json:"body"`
	Fields map[string]string `json:"fields,omitempty"`
}

// sendWebhook 经注入的 *http.Client POST JSON 到渠道 URL。
//   - SSRF 收口:投递前再校验一次 URL(拒云元数据/链路本地),与保存时一致。
//   - 超时:由注入的 client.Timeout 兜底;另套 8s context(取较短者)。
//   - 非 2xx → 人读错误(含状态码,绝无敏感字段);响应体读取限量。
func (s *service) sendWebhook(ctx context.Context, ch *Channel, payload Payload) error {
	target := ch.Config.URL
	if !validWebhookURL(target) {
		return fmt.Errorf("webhook 地址不被允许:仅 http/https,且不可指向云元数据/链路本地地址")
	}

	body, err := json.Marshal(webhookEnvelope{
		Title:  payload.Title,
		Body:   payload.Body,
		Fields: payload.Fields,
	})
	if err != nil {
		return fmt.Errorf("webhook 载荷序列化失败")
	}

	// 套 8s 超时 context(与注入 client.Timeout 共同兜底,取先到者)。
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook 请求构造失败:地址可能非法")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Pipewright-notify/1")

	resp, err := s.client.Do(req)
	if err != nil {
		return mapWebhookTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	// 读弃少量响应体以复用连接;限量防撑爆。
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxWebhookRespBytes))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("webhook 接收端返回非 2xx 状态:HTTP %d", resp.StatusCode)
}

// mapWebhookTransportError 把传输层错误映射为人读错误(超时/连接失败;绝无敏感字段/URL 细节)。
func mapWebhookTransportError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("webhook 连接超时:接收端无响应")
	}
	msg := err.Error()
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "Client.Timeout") {
		return fmt.Errorf("webhook 连接超时:接收端无响应")
	}
	return fmt.Errorf("webhook 连接失败:无法连接到接收端")
}

// validWebhookURL 对 webhook 目标 URL 做 SSRF 收口:scheme 仅 http/https;**恒拒云元数据
// (169.254.169.254,链路本地)/链路本地/未指定地址**(最高价值 SSRF 目标)。
// 私网放行(自托管内网接收器,如运维内网告警网关)——与仓库既定「放行私网」自托管姿态一致
// (单管理员对内网本就有访问,不构成提权);回环亦放行(本机起接收器自测)。
func validWebhookURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	blocked := func(ip net.IP) bool {
		// 云元数据 169.254.169.254 / 链路本地 / 未指定:恒拒(回环、私网放行)。
		return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
	}
	if ip := net.ParseIP(host); ip != nil {
		return !blocked(ip)
	}
	addrs, lerr := net.LookupIP(host)
	if lerr != nil || len(addrs) == 0 {
		return true // DNS 抖动不误拒;投递自身会自然失败
	}
	for _, ip := range addrs {
		if blocked(ip) {
			return false
		}
	}
	return true
}
