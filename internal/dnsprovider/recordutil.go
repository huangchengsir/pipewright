package dnsprovider

import (
	"errors"
	"fmt"
	"strings"
)

// subDomain 把 name(FQDN 或已是相对子域)算成相对 zone 的子域名(DNSPod / 阿里云的 RR/sub_domain)。
//
// 规则(大小写不敏感,统一小写处理后比较):
//   - name == zone(域名顶点) → "@"
//   - name 以 ".zone" 结尾 → 去掉该后缀的前缀部分(如 app-x.example.com / example.com → "app-x")
//   - name 不含 zone 后缀(已是相对子域,如 "app-x") → 原样 trim 后返回;空 → "@"
//
// 返回值不带末尾点,顶点固定为 "@"(DNSPod 与 阿里云 AddDomainRecord 均以 "@" 表顶点)。
func subDomain(name, zone string) string {
	n := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(name, ".")))
	z := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(zone, ".")))
	if n == "" || n == z {
		return "@"
	}
	if z != "" && strings.HasSuffix(n, "."+z) {
		rr := strings.TrimSuffix(n, "."+z)
		if rr == "" {
			return "@"
		}
		return rr
	}
	// 已是相对子域(不含 zone 后缀)。
	return n
}

// wrapEnsure 把 verify 路径的错误重打成 ErrEnsureRecord 家族(保留人话尾巴,不泄漏凭据)。
// post() 内部统一用 ErrVerifyFailed 包网络/解析错误;在建记录上下文里改挂 ErrEnsureRecord 以便上层归类。
func wrapEnsure(err error) error {
	if err == nil {
		return nil
	}
	// 凭据格式错误是确定性的,原样上抛(供 httpapi 映射 400)。
	if errors.Is(err, ErrInvalidCredential) {
		return err
	}
	if errors.Is(err, ErrVerifyFailed) {
		// 取人话尾巴(": 无法连接 …" 之类),换挂 ErrEnsureRecord。
		tail := strings.TrimPrefix(err.Error(), ErrVerifyFailed.Error())
		tail = strings.TrimPrefix(tail, ":")
		return fmt.Errorf("%w:%s", ErrEnsureRecord, strings.TrimSpace(tail))
	}
	return err
}
