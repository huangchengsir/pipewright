package notify

import (
	"context"
	"fmt"
	"net"
	"net/smtp"
	"sort"
	"strings"
)

// emailSender 抽象 SMTP 发信(默认 net/smtp;单测注入 stub,无需真起 SMTP server)。
//   - addr:"host:port"。
//   - auth:smtp.Auth(无密码时为 nil)。
//   - from:发件人。
//   - to:收件人列表。
//   - msg:RFC 822 消息体(含头)。
type emailSender func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error

// smtpSend 是默认基于 net/smtp 的发信实现。
func smtpSend(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return smtp.SendMail(addr, auth, from, to, msg)
}

// sendEmail 经 SMTP 发一封通知邮件。
//   - 敏感字段(SMTP 密码):仅在此处从密文解密、构造 PlainAuth 后用完即弃,绝不日志/回显。
//   - 无密码(sealed 空)→ 无认证发信(本机 stub / 内网开放中继)。
//   - 失败 → 人读错误(绝无密码明文)。
func (s *service) sendEmail(_ context.Context, ch *Channel, sealed []byte, payload Payload) error {
	cfg := ch.Config
	if cfg.SMTPHost == "" || cfg.SMTPPort <= 0 || cfg.From == "" || cfg.To == "" {
		return fmt.Errorf("email 渠道配置不完整:需 smtpHost / smtpPort / from / to")
	}

	var auth smtp.Auth
	if len(sealed) > 0 {
		password, err := s.openSecret(sealed)
		if err != nil {
			return fmt.Errorf("email 凭据不可读:保险库未配置或密文损坏")
		}
		username := cfg.Username
		if username == "" {
			username = cfg.From
		}
		auth = smtp.PlainAuth("", username, password, cfg.SMTPHost)
		// password 局部变量随函数返回出栈;不留副本、不日志。
	}

	recipients := splitRecipients(cfg.To)
	if len(recipients) == 0 {
		return fmt.Errorf("email 渠道无有效收件人")
	}

	msg := buildMessage(cfg.From, recipients, payload)
	addr := net.JoinHostPort(cfg.SMTPHost, fmt.Sprintf("%d", cfg.SMTPPort))

	if err := s.sendMail(addr, auth, cfg.From, recipients, msg); err != nil {
		return mapSMTPError(err)
	}
	return nil
}

// splitRecipients 把逗号/分号分隔的收件人串拆为列表(trim 空项)。
func splitRecipients(to string) []string {
	parts := strings.FieldsFunc(to, func(r rune) bool { return r == ',' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// sanitizeHeader 去除邮件头值里的 CR/LF(code-review P4:防 SMTP 头注入)。
// Subject 来自用户模板渲染输出、From/To 来自渠道配置;任一含 `\r`/`\n` 可注入伪造头
// (Bcc/Content-Type/提前结束头区注入正文)。头值里换行一律剥除(替换为空格)。
func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// buildMessage 构造极简 RFC 822 邮件(纯文本正文 + Fields 附加)。
func buildMessage(from string, to []string, payload Payload) []byte {
	subject := payload.Title
	if subject == "" {
		subject = "Pipewright 通知"
	}
	subject = sanitizeHeader(subject)
	safeFrom := sanitizeHeader(from)
	safeTo := make([]string, len(to))
	for i, r := range to {
		safeTo[i] = sanitizeHeader(r)
	}
	var b strings.Builder
	b.WriteString("From: " + safeFrom + "\r\n")
	b.WriteString("To: " + strings.Join(safeTo, ", ") + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(payload.Body)
	if len(payload.Fields) > 0 {
		b.WriteString("\r\n\r\n")
		// 稳定字段顺序(测试可重现)。
		keys := make([]string, 0, len(payload.Fields))
		for k := range payload.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(k + ": " + payload.Fields[k] + "\r\n")
		}
	}
	return []byte(b.String())
}

// mapSMTPError 把 SMTP 发信错误映射为人读错误(绝无密码明文)。
func mapSMTPError(err error) error {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "auth"):
		return fmt.Errorf("email 发送失败:SMTP 认证被拒,请检查用户名/密码")
	case strings.Contains(low, "timeout") || strings.Contains(low, "deadline"):
		return fmt.Errorf("email 发送失败:连接 SMTP 服务器超时")
	case strings.Contains(low, "connection refused") || strings.Contains(low, "no such host") || strings.Contains(low, "dial"):
		return fmt.Errorf("email 发送失败:无法连接到 SMTP 服务器")
	default:
		return fmt.Errorf("email 发送失败:SMTP 服务器返回错误")
	}
}
