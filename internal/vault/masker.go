package vault

import "strings"

// 掩码服务端计算:列表/响应只暴露掩码值,绝不回显明文。
// 掩码字符串本身只含「极少量」可识别尾部/前缀,不足以还原密钥。
//
//   - git_token : 末 4 位可见,前缀点掩码 → "ghp_••••a91f"(保留原 token 前缀如 ghp_/gho_)
//   - ssh_key   : 算法前缀可见 + 私钥末 4 位 → "ssh-ed25519 ••••Qz1k"
//   - registry  : 用户名可见 + 密码掩码 → "alice ••••"(格式 user:password 或仅 user)
//
// 掩码点用 U+2022 BULLET。

const maskDots = "••••"

// maskMinLen 是「可暴露任何尾部/前缀」的最小明文长度门槛。
// secret 短于此阈值时全打点,绝不暴露任何尾部或前缀(避免短 token 掩码=明文)。
const maskMinLen = 12

// allowedTokenPrefixes 是允许在 git_token 掩码中暴露的前缀白名单。
// 仅暴露这些众所周知、不含密钥熵的服务前缀;其它一律不暴露前缀(避免泄漏用户自定义前缀里的信息)。
var allowedTokenPrefixes = []string{"github_pat_", "ghp_", "gho_"}

// mask 按类型生成掩码字符串。secret 为明文,仅用于计算可见尾部/前缀,不被存储。
func mask(credType, secret string) string {
	switch credType {
	case TypeGitToken:
		return maskGitToken(secret)
	case TypeSSHKey:
		return maskSSHKey(secret)
	case TypeRegistry:
		return maskRegistry(secret)
	default:
		return maskDots
	}
}

// maskGitToken 仅暴露白名单服务前缀(ghp_/gho_/github_pat_)与末 4 位。
// 明文短于阈值则全打点,不暴露任何前缀/尾部(短 token 掩码绝不等于明文)。
func maskGitToken(secret string) string {
	s := strings.TrimSpace(secret)
	if len([]rune(s)) < maskMinLen {
		return maskDots
	}
	prefix := ""
	for _, p := range allowedTokenPrefixes {
		if strings.HasPrefix(s, p) {
			prefix = p
			break
		}
	}
	tail := lastN(s, 4)
	return prefix + maskDots + tail
}

// maskSSHKey 取算法前缀(OpenSSH 公钥格式首段,如 ssh-ed25519 / ssh-rsa);
// 若为私钥 PEM(无算法前缀),则前缀留空。末 4 位取**去除尾部空白后**的最后 4 个非空白字符。
func maskSSHKey(secret string) string {
	s := strings.TrimSpace(secret)
	// 最小长度门槛:极短密钥全打点,不暴露任何尾部/前缀。
	// 以密钥体(去标点空白后的字母数字)长度判定,PEM 标点不计入。
	if len(alnumOnly(s)) < maskMinLen {
		return maskDots
	}
	algo := ""
	if strings.HasPrefix(s, "ssh-") {
		if i := strings.IndexAny(s, " \t"); i > 0 {
			algo = s[:i] + " "
		}
	}
	// 末 4 位:仅取末尾的 base64 字母数字(对 PEM 私钥跳过 -----END----- 等标点,
	// 只暴露密钥体的 4 个字符,且绝不含 BEGIN/END 头)。
	tail := lastNAlnum(s, 4)
	return algo + maskDots + tail
}

// maskRegistry 掩码镜像仓库凭据。
//   - "username:password" 形式:暴露用户名(非密钥),掩码密码 → "alice ••••"。
//   - 无冒号(纯 token / 仅密码,如 Docker Hub / ACR 访问令牌):**整串视为敏感**,
//     按长度门槛只暴露末 4 位,短于阈值全打点。绝不回显整串明文(AC-SEC-01/06)。
func maskRegistry(secret string) string {
	s := strings.TrimSpace(secret)
	if i := strings.Index(s, ":"); i >= 0 {
		user := s[:i]
		return user + " " + maskDots
	}
	if len([]rune(s)) < maskMinLen {
		return maskDots
	}
	return maskDots + lastN(s, 4)
}

// lastN 返回 s 末尾 n 个字符(按 rune 安全);不足 n 个则返回全部。
func lastN(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return string(r)
	}
	return string(r[len(r)-n:])
}

// alnumOnly 返回 s 中所有 ASCII 字母/数字字符(去标点/空白),用于度量密钥体长度。
func alnumOnly(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		}
	}
	return string(out)
}

// lastNAlnum 返回 s 末尾的 n 个 ASCII 字母/数字(从右扫描,跳过标点/空白)。
// 用于 SSH 私钥 PEM:只暴露密钥体尾部字符,绝不含 ----- 等 PEM 标点。
func lastNAlnum(s string, n int) string {
	out := make([]byte, 0, n)
	for i := len(s) - 1; i >= 0 && len(out) < n; i-- {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			out = append([]byte{c}, out...)
		}
	}
	return string(out)
}
