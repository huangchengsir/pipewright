package dnsprovider

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// --- 凭据解析 ---------------------------------------------------------------

func TestParseAliCred(t *testing.T) {
	if _, _, err := parseAliCred("no-comma"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("无逗号应 ErrInvalidCredential, got %v", err)
	}
	if _, _, err := parseAliCred(",sk"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("空 ak 应 ErrInvalidCredential, got %v", err)
	}
	if _, _, err := parseAliCred("ak,"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("空 sk 应 ErrInvalidCredential, got %v", err)
	}
	ak, sk, err := parseAliCred("  LTAI123 , secretVal  ")
	if err != nil || ak != "LTAI123" || sk != "secretVal" {
		t.Fatalf("应 trim 切分, got %q/%q/%v", ak, sk, err)
	}
}

// --- 签名已知向量(手算钉死)-------------------------------------------------
// 用固定 nonce/timestamp + 已知 ak/sk,断言签名等于离线计算值。
// 任何编码/排序/HMAC 偏差都会让此用例红。

func TestAliSignKnownVector(t *testing.T) {
	p := url.Values{}
	p.Set("Action", "DescribeDomainInfo")
	p.Set("DomainName", "example.com")
	p.Set("Version", alidnsVersion)
	p.Set("AccessKeyId", "testAccessKeyId")
	p.Set("SignatureMethod", alidnsSignatureMethod)
	p.Set("SignatureVersion", alidnsSignatureVersion)
	p.Set("SignatureNonce", "fixednonce123")
	p.Set("Timestamp", "2026-01-02T03:04:05Z")
	p.Set("Format", alidnsFormat)

	got := aliSign(p, "testAccessKeySecret")
	const want = "PnKQ2LAKo3Ir99er2fVAFxRJjrQ="
	if got != want {
		t.Fatalf("签名不符已知向量\n got=%q\nwant=%q", got, want)
	}
}

func TestAliPercentEncode(t *testing.T) {
	// 阿里云特例:空格→%20、*→%2A、~ 不编码、: 编码为 %3A。
	if aliPercentEncode("2026-01-02T03:04:05Z") != "2026-01-02T03%3A04%3A05Z" {
		t.Fatalf(": 应编码为 %%3A, got %q", aliPercentEncode("2026-01-02T03:04:05Z"))
	}
	if aliPercentEncode("a b") != "a%20b" {
		t.Fatalf("空格应编码为 %%20")
	}
	if aliPercentEncode("a*b") != "a%2Ab" {
		t.Fatalf("* 应编码为 %%2A, got %q", aliPercentEncode("a*b"))
	}
	if aliPercentEncode("~") != "~" {
		t.Fatalf("~ 不应编码, got %q", aliPercentEncode("~"))
	}
	if aliPercentEncode("/") != "%2F" {
		t.Fatalf("/ 应编码为 %%2F, got %q", aliPercentEncode("/"))
	}
}

// fixedClient 造一个钉死 nonce/timestamp 的阿里云客户端(供请求流断言)。
func fixedAliClient(cred string, rt http.RoundTripper) *alidnsClient {
	c := newAliDNSClient(cred, rt, "")
	c.nowFn = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	c.nonceFn = func() string { return "fixednonce123" }
	return c
}

// --- VerifyZone -------------------------------------------------------------

func TestAliDNSVerifyZoneSuccess(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if q.Get("Action") != "DescribeDomainInfo" {
			t.Fatalf("应 Action=DescribeDomainInfo, got %q", q.Get("Action"))
		}
		if q.Get("DomainName") != "example.com" {
			t.Fatalf("应带 DomainName, got %q", q.Get("DomainName"))
		}
		if q.Get("Signature") == "" {
			t.Fatalf("应带 Signature")
		}
		if q.Get("AccessKeyId") != "testAccessKeyId" {
			t.Fatalf("应带 AccessKeyId, got %q", q.Get("AccessKeyId"))
		}
		return jsonResp(200, `{"DomainName":"example.com","RequestId":"r1"}`), nil
	})
	c := fixedAliClient("testAccessKeyId,testAccessKeySecret", rt)
	if err := c.VerifyZone(context.Background(), "example.com"); err != nil {
		t.Fatalf("VerifyZone: %v", err)
	}
}

func TestAliDNSVerifyZoneAuthFail(t *testing.T) {
	rt := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResp(403, `{"Code":"InvalidAccessKeyId.NotFound","Message":"Specified access key is not found.","RequestId":"r2"}`), nil
	})
	c := fixedAliClient("testAccessKeyId,wrong-secret", rt)
	err := c.VerifyZone(context.Background(), "example.com")
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("鉴权失败应 ErrVerifyFailed, got %v", err)
	}
	if strings.Contains(err.Error(), "wrong-secret") || strings.Contains(err.Error(), "testAccessKeyId") {
		t.Fatalf("错误文本不应含凭据: %v", err)
	}
}

func TestAliDNSVerifyZoneNotFound(t *testing.T) {
	rt := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResp(400, `{"Code":"InvalidDomainName.NoExist","Message":"The specified domain name does not exist.","RequestId":"r3"}`), nil
	})
	c := fixedAliClient("testAccessKeyId,testAccessKeySecret", rt)
	if err := c.VerifyZone(context.Background(), "nope.com"); !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("域名不存在应 ErrVerifyFailed, got %v", err)
	}
}

func TestAliDNSInvalidCredential(t *testing.T) {
	called := false
	rt := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		called = true
		return jsonResp(200, `{}`), nil
	})
	c := fixedAliClient("no-comma", rt)
	if err := c.VerifyZone(context.Background(), "example.com"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("坏凭据应 ErrInvalidCredential, got %v", err)
	}
	if called {
		t.Fatalf("坏凭据不应发任何请求")
	}
}

// --- EnsureARecord ----------------------------------------------------------

func TestAliDNSEnsureARecordAdd(t *testing.T) {
	var added bool
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		switch q.Get("Action") {
		case "DescribeDomainRecords":
			if q.Get("RRKeyWord") != "app-abc" {
				t.Fatalf("List 应带 RRKeyWord=app-abc, got %q", q.Get("RRKeyWord"))
			}
			if q.Get("Type") != "A" {
				t.Fatalf("List 应带 Type=A, got %q", q.Get("Type"))
			}
			return jsonResp(200, `{"DomainRecords":{"Record":[]}}`), nil
		case "AddDomainRecord":
			added = true
			if q.Get("RR") != "app-abc" {
				t.Fatalf("Add 应带 RR=app-abc, got %q", q.Get("RR"))
			}
			if q.Get("Value") != "203.0.113.5" {
				t.Fatalf("Add 应带目标 IP, got %q", q.Get("Value"))
			}
			return jsonResp(200, `{"RecordId":"rec-new"}`), nil
		}
		t.Fatalf("未预期 Action %q", q.Get("Action"))
		return nil, nil
	})
	c := fixedAliClient("testAccessKeyId,testAccessKeySecret", rt)
	if err := c.EnsureARecord(context.Background(), "example.com", "app-abc.example.com", "203.0.113.5"); err != nil {
		t.Fatalf("EnsureARecord: %v", err)
	}
	if !added {
		t.Fatalf("无既有记录应 AddDomainRecord")
	}
}

func TestAliDNSEnsureARecordUpdate(t *testing.T) {
	var updatedID string
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		switch q.Get("Action") {
		case "DescribeDomainRecords":
			return jsonResp(200, `{"DomainRecords":{"Record":[{"RecordId":"rec-7","RR":"app-abc","Type":"A","Value":"1.2.3.4"}]}}`), nil
		case "UpdateDomainRecord":
			updatedID = q.Get("RecordId")
			if q.Get("Value") != "203.0.113.9" {
				t.Fatalf("Update 应带新 IP, got %q", q.Get("Value"))
			}
			return jsonResp(200, `{"RecordId":"rec-7"}`), nil
		}
		t.Fatalf("未预期 Action %q", q.Get("Action"))
		return nil, nil
	})
	c := fixedAliClient("testAccessKeyId,testAccessKeySecret", rt)
	if err := c.EnsureARecord(context.Background(), "example.com", "app-abc.example.com", "203.0.113.9"); err != nil {
		t.Fatalf("EnsureARecord: %v", err)
	}
	if updatedID != "rec-7" {
		t.Fatalf("应 Update RecordId=rec-7, got %q", updatedID)
	}
}

func TestAliDNSEnsureARecordNoOp(t *testing.T) {
	var mutated bool
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		switch q.Get("Action") {
		case "DescribeDomainRecords":
			return jsonResp(200, `{"DomainRecords":{"Record":[{"RecordId":"rec-7","RR":"app-abc","Type":"A","Value":"203.0.113.5"}]}}`), nil
		case "AddDomainRecord", "UpdateDomainRecord":
			mutated = true
			return jsonResp(200, `{}`), nil
		}
		t.Fatalf("未预期 Action %q", q.Get("Action"))
		return nil, nil
	})
	c := fixedAliClient("testAccessKeyId,testAccessKeySecret", rt)
	if err := c.EnsureARecord(context.Background(), "example.com", "app-abc.example.com", "203.0.113.5"); err != nil {
		t.Fatalf("EnsureARecord: %v", err)
	}
	if mutated {
		t.Fatalf("值相同应幂等 no-op")
	}
}

// --- 经 AllocateSubdomain 端到端(DNSPod / 阿里云走真实客户端 + mock transport)----------

func TestAllocateSubdomainDNSPodReal(t *testing.T) {
	ctx := context.Background()
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/Record.List"):
			return jsonResp(200, `{"status":{"code":"10"},"records":[]}`), nil
		case strings.Contains(r.URL.Path, "/Record.Create"):
			return jsonResp(200, `{"status":{"code":"1"}}`), nil
		}
		t.Fatalf("未预期 %s", r.URL.Path)
		return nil, nil
	})
	rc := &stubRouteCreator{nextRouteID: "route-dp"}
	svc := newTestService(t, stubVault{tokens: map[string]string{"cred-1": "12345,sEcReT"}}, rc, dialDNS(rt))
	p, _ := svc.Create(ctx, CreateInput{Type: "dnspod", Name: "DP", CredentialID: "cred-1", BaseDomain: "example.com"})
	ref, err := svc.AllocateSubdomain(ctx, AllocateInput{
		ProviderID: p.ID, ServerID: "srv-1", UpstreamContainer: "web", UpstreamPort: 8080, HostIP: "203.0.113.5",
	})
	if err != nil {
		t.Fatalf("AllocateSubdomain(dnspod): %v", err)
	}
	if ref.RouteID != "route-dp" {
		t.Fatalf("应建路由, got %q", ref.RouteID)
	}
}
