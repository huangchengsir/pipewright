package dnsprovider

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// alidnsAPIBase 是阿里云 DNS(Alidns)RPC API 根。测试经 RoundTripper 拦截,不触真网络。
const alidnsAPIBase = "https://alidns.aliyuncs.com/"

// 阿里云 RPC 公共参数固定值。
const (
	alidnsVersion          = "2015-01-09"
	alidnsSignatureMethod  = "HMAC-SHA1"
	alidnsSignatureVersion = "1.0"
	alidnsFormat           = "JSON"
)

// 凭据约定(vault 单字串存储模型不变):阿里云用 "accessKeyId,accessKeySecret"。
// 即在保险库里存 "LTAI...,abcdefSecret" 这样一个逗号分隔字串;按**第一个逗号**切分,两侧 trim。
// 无逗号 / 任一段空 → ErrInvalidCredential。绝不把 ak/sk 任何片段回显到错误/日志/DTO。
type alidnsClient struct {
	cred string // 形如 "accessKeyId,accessKeySecret";仅进程内,不入库/日志/响应
	hc   *http.Client
	base string

	// 以下供测试注入,钉死签名(生产为 nil → 用 time.Now / crypto-rand nonce)。
	nowFn   func() time.Time
	nonceFn func() string
}

// newAliDNSClient 构造阿里云 DNS 客户端。cred 为保险库里存的 "accessKeyId,accessKeySecret" 原串。
// transport 为 nil 用默认(带超时),测试可注入 mock RoundTripper。
func newAliDNSClient(cred string, transport http.RoundTripper, base string) *alidnsClient {
	hc := &http.Client{Timeout: 15 * time.Second}
	if transport != nil {
		hc.Transport = transport
	}
	if base == "" {
		base = alidnsAPIBase
	}
	return &alidnsClient{cred: strings.TrimSpace(cred), hc: hc, base: base}
}

// parseAliCred 校验 "accessKeyId,accessKeySecret" 形态(按第一个逗号切分,两侧 trim)。
// 任一为空 → ErrInvalidCredential。返回 (ak, sk)。
func parseAliCred(raw string) (ak, sk string, err error) {
	idx := strings.IndexByte(raw, ',')
	if idx < 0 {
		return "", "", fmt.Errorf("%w:阿里云凭据须为「AccessKeyId,AccessKeySecret」(逗号分隔)", ErrInvalidCredential)
	}
	ak = strings.TrimSpace(raw[:idx])
	sk = strings.TrimSpace(raw[idx+1:])
	if ak == "" || sk == "" {
		return "", "", fmt.Errorf("%w:阿里云凭据「AccessKeyId,AccessKeySecret」两段均不可为空", ErrInvalidCredential)
	}
	return ak, sk, nil
}

// aliPercentEncode 按阿里云 RPC v1 规则做 RFC3986 百分号编码:
// 用 url.QueryEscape 后,把 "+"→"%20"、"*"→"%2A"、"%7E"→"~"。
func aliPercentEncode(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	e = strings.ReplaceAll(e, "%7E", "~")
	return e
}

// aliSign 据 RPC v1 规范对已含全部公共/业务参数(不含 Signature)的 params 计算签名,
// 返回签名串(base64)。签名方法:
//
//	sortedQuery = 按 key 排序后 percentEncode(k)=percentEncode(v) 用 & 连接
//	stringToSign = "GET" + "&" + percentEncode("/") + "&" + percentEncode(sortedQuery)
//	signature   = base64(HMAC-SHA1(key=secret+"&", stringToSign))
func aliSign(params url.Values, accessKeySecret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(aliPercentEncode(k))
		b.WriteByte('=')
		b.WriteString(aliPercentEncode(params.Get(k)))
	}
	canonical := b.String()

	stringToSign := "GET&" + aliPercentEncode("/") + "&" + aliPercentEncode(canonical)

	mac := hmac.New(sha1.New, []byte(accessKeySecret+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// aliErrResp 是阿里云 RPC 错误信封(成功响应无 Code/Message;HTTP 4xx/5xx 才有)。
type aliErrResp struct {
	Code      string `json:"Code"`
	Message   string `json:"Message"`
	RequestID string `json:"RequestId"`
}

// aliRecord / aliDescribeRecordsResp 取我们需要的字段子集。
type aliRecord struct {
	RecordID string `json:"RecordId"`
	RR       string `json:"RR"`
	Type     string `json:"Type"`
	Value    string `json:"Value"`
}

type aliDescribeRecordsResp struct {
	DomainRecords struct {
		Record []aliRecord `json:"Record"`
	} `json:"DomainRecords"`
}

func (c *alidnsClient) now() time.Time {
	if c.nowFn != nil {
		return c.nowFn()
	}
	return time.Now().UTC()
}

func (c *alidnsClient) nonce() string {
	if c.nonceFn != nil {
		return c.nonceFn()
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 退化:用时间纳秒拼一个仍唯一的 nonce(签名仍正确,只是熵略低)。
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// call 发一个签名后的 GET 请求(action + 业务参数),把成功响应体解到 out。
// HTTP 非 2xx → 解阿里云错误信封映射人话(绝不含 ak/sk)。baseErr 决定错误家族。
func (c *alidnsClient) call(ctx context.Context, baseErr error, action string, biz url.Values, out any) error {
	ak, sk, err := parseAliCred(c.cred)
	if err != nil {
		return err
	}

	params := url.Values{}
	for k, v := range biz {
		params[k] = v
	}
	params.Set("Action", action)
	params.Set("Version", alidnsVersion)
	params.Set("AccessKeyId", ak)
	params.Set("SignatureMethod", alidnsSignatureMethod)
	params.Set("SignatureVersion", alidnsSignatureVersion)
	params.Set("SignatureNonce", c.nonce())
	params.Set("Timestamp", c.now().UTC().Format("2006-01-02T15:04:05Z"))
	params.Set("Format", alidnsFormat)

	sig := aliSign(params, sk)
	params.Set("Signature", sig)

	endpoint := c.base + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w:构造阿里云请求失败", baseErr)
	}
	req.Header.Set("User-Agent", "Pipewright/1.0 (dns@pipewright.local)")

	resp, err := c.hc.Do(req)
	if err != nil {
		// 绝不回显 err.Error()(可能含 URL/签名);给固定人话。
		return fmt.Errorf("%w:无法连接阿里云 DNS API", baseErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var ae aliErrResp
		_ = json.NewDecoder(resp.Body).Decode(&ae)
		return aliErr(baseErr, resp.StatusCode, ae)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("%w:阿里云 DNS API 响应解析失败", baseErr)
		}
	}
	return nil
}

// aliErr 把阿里云错误信封映射人话(绝不含 ak/sk;阿里云 Code/Message 不含凭据明文)。
func aliErr(base error, status int, ae aliErrResp) error {
	switch ae.Code {
	case "InvalidAccessKeyId.NotFound", "SignatureDoesNotMatch", "Forbidden.AccessKeyDisabled", "Forbidden.RAM":
		return fmt.Errorf("%w:阿里云凭据无效或无权限", base)
	case "InvalidDomainName.NoExist", "DomainRecordDuplicate", "InvalidDomainName.Unsupported", "DomainNotExists":
		return fmt.Errorf("%w:阿里云域名不存在或不受支持", base)
	}
	if ae.Code != "" {
		return fmt.Errorf("%w:阿里云 DNS API 返回错误(HTTP %d, %s)", base, status, ae.Code)
	}
	return fmt.Errorf("%w:阿里云 DNS API 拒绝(HTTP %d)", base, status)
}

// VerifyZone 校验当前凭据可管理 zone:Action=DescribeDomainInfo&DomainName=<zone>。
// HTTP 200 + 无错误码 → 成功。
func (c *alidnsClient) VerifyZone(ctx context.Context, zone string) error {
	return c.call(ctx, ErrVerifyFailed, "DescribeDomainInfo", url.Values{"DomainName": {zone}}, nil)
}

// EnsureARecord 为 zone 下 name 建/改 A 记录指向 ip(幂等 upsert):
//  1. DescribeDomainRecords(DomainName=zone, RRKeyWord=<sub>, Type=A)找既有记录。
//  2. 找到且 value 不同 → UpdateDomainRecord;相同 → no-op;没找到 → AddDomainRecord(RR=<sub>)。
//
// sub(RR)相对 zone 计算,顶点 → "@"。
func (c *alidnsClient) EnsureARecord(ctx context.Context, zone, name, ip string) error {
	sub := subDomain(name, zone)

	var list aliDescribeRecordsResp
	listFields := url.Values{
		"DomainName": {zone},
		"RRKeyWord":  {sub},
		"Type":       {"A"},
	}
	if err := c.call(ctx, ErrEnsureRecord, "DescribeDomainRecords", listFields, &list); err != nil {
		return err
	}

	// RRKeyWord 是模糊匹配,精确找 RR==sub 的 A 记录。
	var existing *aliRecord
	for i := range list.DomainRecords.Record {
		r := list.DomainRecords.Record[i]
		if strings.EqualFold(r.Type, "A") && strings.EqualFold(r.RR, sub) {
			existing = &list.DomainRecords.Record[i]
			break
		}
	}

	if existing != nil {
		if existing.Value == ip {
			return nil // 幂等 no-op。
		}
		updFields := url.Values{
			"RecordId": {existing.RecordID},
			"RR":       {sub},
			"Type":     {"A"},
			"Value":    {ip},
		}
		return c.call(ctx, ErrEnsureRecord, "UpdateDomainRecord", updFields, nil)
	}

	addFields := url.Values{
		"DomainName": {zone},
		"RR":         {sub},
		"Type":       {"A"},
		"Value":      {ip},
	}
	return c.call(ctx, ErrEnsureRecord, "AddDomainRecord", addFields, nil)
}
