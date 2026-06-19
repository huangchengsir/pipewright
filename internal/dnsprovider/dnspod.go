package dnsprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// dnspodAPIBase 是腾讯云 DNSPod 经典 API(dnsapi.cn)根。测试经 RoundTripper 拦截,不触真网络。
const dnspodAPIBase = "https://dnsapi.cn"

// 凭据约定(vault 单字串存储模型不变):DNSPod 用经典 login_token = "<ID>,<Token>"。
// 即在保险库里存 "12345,abcdef0123456789..." 这样一个逗号分隔字串;客户端按**第一个逗号**切分,
// 两侧 trim 空白。无逗号 → ErrInvalidCredential。绝不把 token 任何片段回显到错误/日志/DTO。
type dnspodClient struct {
	loginToken string // 形如 "<id>,<token>";仅进程内,不入库/日志/响应
	hc         *http.Client
	base       string
}

// newDNSPodClient 构造 DNSPod 客户端。loginToken 为保险库里存的 "id,token" 原串(校验留给请求时,
// 以便统一映射 ErrInvalidCredential)。transport 为 nil 时用默认(带超时),测试可注入 mock。
func newDNSPodClient(loginToken string, transport http.RoundTripper, base string) *dnspodClient {
	hc := &http.Client{Timeout: 15 * time.Second}
	if transport != nil {
		hc.Transport = transport
	}
	if base == "" {
		base = dnspodAPIBase
	}
	return &dnspodClient{loginToken: strings.TrimSpace(loginToken), hc: hc, base: strings.TrimRight(base, "/")}
}

// parseDNSPodToken 校验 "id,token" 形态(按第一个逗号切分,两侧 trim);任一为空 → ErrInvalidCredential。
// 返回归一化后的 login_token("id,token",已 trim),供表单字段直接使用。
func parseDNSPodToken(raw string) (loginToken string, err error) {
	idx := strings.IndexByte(raw, ',')
	if idx < 0 {
		return "", fmt.Errorf("%w:DNSPod 凭据须为「ID,Token」(逗号分隔)", ErrInvalidCredential)
	}
	id := strings.TrimSpace(raw[:idx])
	tok := strings.TrimSpace(raw[idx+1:])
	if id == "" || tok == "" {
		return "", fmt.Errorf("%w:DNSPod 凭据「ID,Token」两段均不可为空", ErrInvalidCredential)
	}
	return id + "," + tok, nil
}

// dnspodStatus 是 DNSPod 经典 API 的通用 status 信封。code=="1" 为成功。
type dnspodStatus struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// dpDomainInfoResp / dpRecordListResp / dpMutateResp 取我们需要的字段子集。
type dpDomainInfoResp struct {
	Status dnspodStatus `json:"status"`
}

type dpRecord struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type dpRecordListResp struct {
	Status  dnspodStatus `json:"status"`
	Records []dpRecord   `json:"records"`
}

type dpMutateResp struct {
	Status dnspodStatus `json:"status"`
}

// post 发一个 form-POST 到 dnsapi.cn,自动带 login_token / format=json。错误绝不含 token。
// out 为解信封目标(传入指针)。
func (c *dnspodClient) post(ctx context.Context, path string, fields url.Values, out any) error {
	loginToken, err := parseDNSPodToken(c.loginToken)
	if err != nil {
		return err
	}
	form := url.Values{}
	for k, v := range fields {
		form[k] = v
	}
	form.Set("login_token", loginToken)
	form.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("%w:构造 DNSPod 请求失败", ErrVerifyFailed)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// DNSPod 经典 API 要求带 UserAgent(否则可能拒绝)。
	req.Header.Set("User-Agent", "Pipewright/1.0 (dns@pipewright.local)")

	resp, err := c.hc.Do(req)
	if err != nil {
		// 绝不回显 err.Error()(可能含 URL/token);给固定人话。
		return fmt.Errorf("%w:无法连接 DNSPod API", ErrVerifyFailed)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w:DNSPod API 拒绝(HTTP %d)", ErrVerifyFailed, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%w:DNSPod API 响应解析失败", ErrVerifyFailed)
	}
	return nil
}

// dpStatusErr 把非成功的 status 映射成人话错误(绝不含 token);DNSPod 的 message 不含凭据。
func dpStatusErr(base error, st dnspodStatus) error {
	msg := strings.TrimSpace(st.Message)
	// 常见鉴权失败码(login_token 无效 / 已删):给更明确的提示。
	switch st.Code {
	case "-1", "-2", "-7", "-8", "85":
		return fmt.Errorf("%w:DNSPod 凭据无效或无权限", base)
	}
	if msg != "" {
		return fmt.Errorf("%w:DNSPod API 返回错误(code %s):%s", base, st.Code, msg)
	}
	return fmt.Errorf("%w:DNSPod API 返回错误(code %s)", base, st.Code)
}

// VerifyZone 校验当前 login_token 可管理 zone(根域):POST Domain.Info。code=="1" 成功。
func (c *dnspodClient) VerifyZone(ctx context.Context, zone string) error {
	var out dpDomainInfoResp
	if err := c.post(ctx, "/Domain.Info", url.Values{"domain": {zone}}, &out); err != nil {
		return err
	}
	if out.Status.Code != "1" {
		return dpStatusErr(ErrVerifyFailed, out.Status)
	}
	return nil
}

// EnsureARecord 为 zone 下的 name 建/改 A 记录指向 ip(幂等 upsert):
//  1. Record.List(sub_domain=<name 相对 zone>, record_type=A)找既有记录。
//  2. 找到且 value 不同 → Record.Modify;找到且相同 → no-op;没找到 → Record.Create。
//
// name 可能是 FQDN 也可能是相对 sub_domain;此处统一算出相对 zone 的 sub_domain(顶点 → "@")。
func (c *dnspodClient) EnsureARecord(ctx context.Context, zone, name, ip string) error {
	sub := subDomain(name, zone)

	var list dpRecordListResp
	listFields := url.Values{
		"domain":      {zone},
		"sub_domain":  {sub},
		"record_type": {"A"},
	}
	if err := c.post(ctx, "/Record.List", listFields, &list); err != nil {
		return wrapEnsure(err)
	}
	// DNSPod 在「无记录」时返回 code "10"(No records)而非空列表;两者都按「不存在」处理。
	if list.Status.Code != "1" && list.Status.Code != "10" {
		return dpStatusErr(ErrEnsureRecord, list.Status)
	}

	// 在返回的记录里找 sub 对应的 A 记录(DNSPod 的 record.Name 是相对 sub_domain,顶点为 "@")。
	var existing *dpRecord
	for i := range list.Records {
		r := list.Records[i]
		if strings.EqualFold(r.Type, "A") && strings.EqualFold(r.Name, sub) {
			existing = &list.Records[i]
			break
		}
	}

	if existing != nil {
		if existing.Value == ip {
			return nil // 已指向目标 IP,幂等 no-op。
		}
		modFields := url.Values{
			"domain":      {zone},
			"record_id":   {existing.ID},
			"sub_domain":  {sub},
			"record_type": {"A"},
			"record_line": {"默认"},
			"value":       {ip},
		}
		var mod dpMutateResp
		if err := c.post(ctx, "/Record.Modify", modFields, &mod); err != nil {
			return wrapEnsure(err)
		}
		if mod.Status.Code != "1" {
			return dpStatusErr(ErrEnsureRecord, mod.Status)
		}
		return nil
	}

	createFields := url.Values{
		"domain":      {zone},
		"sub_domain":  {sub},
		"record_type": {"A"},
		"record_line": {"默认"},
		"value":       {ip},
	}
	var create dpMutateResp
	if err := c.post(ctx, "/Record.Create", createFields, &create); err != nil {
		return wrapEnsure(err)
	}
	if create.Status.Code != "1" {
		return dpStatusErr(ErrEnsureRecord, create.Status)
	}
	return nil
}

// DeleteARecord 删除 zone 下 name 的 A 记录(幂等:无记录返回 nil)。Record.List 找 → Record.Remove。
func (c *dnspodClient) DeleteARecord(ctx context.Context, zone, name string) error {
	sub := subDomain(name, zone)

	var list dpRecordListResp
	listFields := url.Values{
		"domain":      {zone},
		"sub_domain":  {sub},
		"record_type": {"A"},
	}
	if err := c.post(ctx, "/Record.List", listFields, &list); err != nil {
		return fmt.Errorf("%w:%v", ErrDeleteRecord, err)
	}
	if list.Status.Code == "10" {
		return nil // 无记录 → 幂等成功。
	}
	if list.Status.Code != "1" {
		return dpStatusErr(ErrDeleteRecord, list.Status)
	}
	for i := range list.Records {
		r := list.Records[i]
		if strings.EqualFold(r.Type, "A") && strings.EqualFold(r.Name, sub) {
			var rm dpMutateResp
			if err := c.post(ctx, "/Record.Remove", url.Values{"domain": {zone}, "record_id": {r.ID}}, &rm); err != nil {
				return fmt.Errorf("%w:%v", ErrDeleteRecord, err)
			}
			if rm.Status.Code != "1" {
				return dpStatusErr(ErrDeleteRecord, rm.Status)
			}
			return nil
		}
	}
	return nil
}
