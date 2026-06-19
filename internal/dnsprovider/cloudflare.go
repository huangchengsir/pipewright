package dnsprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// cloudflareAPIBase 是 Cloudflare v4 API 根(可在测试中经 RoundTripper 拦截,不触真网络)。
const cloudflareAPIBase = "https://api.cloudflare.com/client/v4"

// cloudflareClient 是经 net/http 直连 Cloudflare API 的 DNSClient 真实实现(无 SDK)。
// token 由调用方从 vault 取出后注入(进程内,用完即弃);本结构体不持久化 token。
type cloudflareClient struct {
	token string
	hc    *http.Client
	base  string
}

// newCloudflareClient 构造 Cloudflare 客户端。transport 为 nil 时用默认(带超时);
// 测试可注入 mock RoundTripper。base 为空时用公网 API 根。
func newCloudflareClient(token string, transport http.RoundTripper, base string) *cloudflareClient {
	hc := &http.Client{Timeout: 15 * time.Second}
	if transport != nil {
		hc.Transport = transport
	}
	if base == "" {
		base = cloudflareAPIBase
	}
	return &cloudflareClient{token: token, hc: hc, base: strings.TrimRight(base, "/")}
}

// cfResponse 是 Cloudflare API 的通用信封(只取我们需要的字段)。
type cfResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

// cfZone / cfRecord 是结果体子集。
type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

// do 发一个带 Bearer token 的请求并解信封;非 2xx / success:false → 人话错误(绝不含 token)。
func (c *cloudflareClient) do(ctx context.Context, method, path string, body any) (*cfResponse, error) {
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("%w:序列化请求体失败", ErrEnsureRecord)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("%w:构造请求失败", ErrVerifyFailed)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		// 绝不回显 err.Error()(可能含 URL/token query);给固定人话。
		return nil, fmt.Errorf("%w:无法连接 Cloudflare API", ErrVerifyFailed)
	}
	defer func() { _ = resp.Body.Close() }()

	var out cfResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w:Cloudflare API 响应解析失败(HTTP %d)", ErrVerifyFailed, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !out.Success {
		return &out, fmt.Errorf("%w:Cloudflare API 拒绝(HTTP %d)%s", ErrVerifyFailed, resp.StatusCode, cfErrMsg(out))
	}
	return &out, nil
}

// cfErrMsg 拼接 Cloudflare 错误信息为人话(绝不含 token;Cloudflare 错误文本不含凭据)。
func cfErrMsg(r cfResponse) string {
	if len(r.Errors) == 0 {
		return ""
	}
	parts := make([]string, 0, len(r.Errors))
	for _, e := range r.Errors {
		parts = append(parts, strings.TrimSpace(e.Message))
	}
	return ":" + strings.Join(parts, "; ")
}

// zoneID 取 zone(根域)的 Cloudflare zone id;无该 zone / token 无权限 → ErrVerifyFailed。
func (c *cloudflareClient) zoneID(ctx context.Context, zone string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(zone), nil)
	if err != nil {
		return "", err
	}
	var zones []cfZone
	if err := json.Unmarshal(resp.Result, &zones); err != nil {
		return "", fmt.Errorf("%w:zone 列表解析失败", ErrVerifyFailed)
	}
	for _, z := range zones {
		if strings.EqualFold(z.Name, zone) {
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("%w:凭据无法管理 zone %s(请确认 token 有该域 DNS 编辑权限)", ErrVerifyFailed, zone)
}

// VerifyZone 校验当前 token 可管理 zone(根域)。
func (c *cloudflareClient) VerifyZone(ctx context.Context, zone string) error {
	_, err := c.zoneID(ctx, zone)
	return err
}

// EnsureARecord 为 zone 下的 name(FQDN)建/改 A 记录指向 ip(幂等 upsert):
// 先查同名 A 记录 → 有则 PUT 更新、无则 POST 新建。
func (c *cloudflareClient) EnsureARecord(ctx context.Context, zone, name, ip string) error {
	zid, err := c.zoneID(ctx, zone)
	if err != nil {
		return err
	}
	// 查同名 A 记录。
	listResp, err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/zones/%s/dns_records?type=A&name=%s", url.PathEscape(zid), url.QueryEscape(name)), nil)
	if err != nil {
		return fmt.Errorf("%w:%s", ErrEnsureRecord, strings.TrimPrefix(err.Error(), ErrVerifyFailed.Error()+":"))
	}
	var records []cfRecord
	if err := json.Unmarshal(listResp.Result, &records); err != nil {
		return fmt.Errorf("%w:记录列表解析失败", ErrEnsureRecord)
	}
	payload := map[string]any{
		"type":    "A",
		"name":    name,
		"content": ip,
		"ttl":     120,
		"proxied": false,
	}
	if len(records) > 0 {
		// 就地更新第一条同名 A 记录。
		if _, err := c.do(ctx, http.MethodPut,
			fmt.Sprintf("/zones/%s/dns_records/%s", url.PathEscape(zid), url.PathEscape(records[0].ID)), payload); err != nil {
			return fmt.Errorf("%w:更新 A 记录失败", ErrEnsureRecord)
		}
		return nil
	}
	if _, err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/zones/%s/dns_records", url.PathEscape(zid)), payload); err != nil {
		return fmt.Errorf("%w:创建 A 记录失败", ErrEnsureRecord)
	}
	return nil
}

// notImplementedClient 是 DNSPod / 阿里云 DNS 的「自动 A 记录」占位实现(诚实地返回未实现)。
// DNS-01 通配符证书签发对这两家仍可用(走 Caddy 镜像内 DNS 插件),与本桩无关。
type notImplementedClient struct{ providerType string }

func (n notImplementedClient) EnsureARecord(context.Context, string, string, string) error {
	return fmt.Errorf("%w(%s 暂未实现自动建 A 记录,请手动添加解析;DNS-01 证书签发仍可用)", ErrProviderNotImplemented, n.providerType)
}

func (n notImplementedClient) VerifyZone(context.Context, string) error {
	return fmt.Errorf("%w(%s 暂未实现 zone 校验)", ErrProviderNotImplemented, n.providerType)
}

// newDNSClient 据提供商类型 + token 构造对应 DNSClient。transport/base 供测试注入(生产为 nil/空)。
func newDNSClient(providerType, token string, transport http.RoundTripper, base string) DNSClient {
	switch providerType {
	case TypeCloudflare:
		return newCloudflareClient(token, transport, base)
	default:
		return notImplementedClient{providerType: providerType}
	}
}
