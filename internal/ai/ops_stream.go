package ai

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/huangchengsir/pipewright/internal/opschat"
)

func (m *opsModel) safeText(text string) string {
	s := m.masker.NewStream()
	return s.Write(text) + s.Finish()
}

func (m *opsModel) pinnedClient(ctx context.Context, base string) (*http.Client, func(), error) {
	destination, err := opsResolve(ctx, base, m.lookupIP)
	if err != nil {
		return nil, nil, opschat.ErrUnavailable
	}
	client := *m.service.client
	client.Timeout = 60 * time.Second
	if m.service.client.Timeout > 0 && m.service.client.Timeout < client.Timeout {
		client.Timeout = m.service.client.Timeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cleanup := func() {}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if original, ok := transport.(*http.Transport); ok {
		pinned := original.Clone()
		pinned.Proxy = nil
		pinned.DialTLS = nil
		pinned.DialTLSContext = nil
		pinned.DisableKeepAlives = true
		if pinned.TLSClientConfig == nil {
			pinned.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		pinned.TLSClientConfig.ServerName = destination.host
		forward, err := destination.forwardProxy(base, original, pinned, m.dialContext)
		if err != nil {
			return nil, nil, err
		}
		if forward != nil {
			client.Transport = forward
		} else {
			dial, err := destination.proxyDial(base, original, m.dialContext)
			if err != nil {
				return nil, nil, err
			}
			pinned.DialContext = destination.dial(dial)
			client.Transport = pinned
		}
		cleanup = pinned.CloseIdleConnections
	}
	return &client, cleanup, nil
}

// Root text only. Tool-plan JSON and reasoning fields are never published.
func planTextPrefix(raw string) string {
	raw = strings.TrimLeft(raw, " \t\r\n")
	if inner, fenced := opsPlanOpening(raw); fenced {
		raw = inner
	}
	d := json.NewDecoder(strings.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return ""
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return ""
		}
		if key != "text" {
			var discard json.RawMessage
			if d.Decode(&discard) != nil {
				return ""
			}
			continue
		}
		rest := strings.TrimLeft(raw[d.InputOffset():], " \t\r\n")
		if !strings.HasPrefix(rest, ":") {
			return ""
		}
		rest = strings.TrimLeft(rest[1:], " \t\r\n")
		if !strings.HasPrefix(rest, `"`) {
			return ""
		}
		rest = rest[1:]
		var out strings.Builder
		for i := 0; i < len(rest); {
			if rest[i] == '"' {
				return out.String()
			}
			if rest[i] == '\\' {
				if i+1 >= len(rest) {
					break
				}
				n := 2
				if rest[i+1] == 'u' {
					n = 6
					if i+n > len(rest) {
						break
					}
					var runeText string
					if json.Unmarshal([]byte(`"`+rest[i:i+n]+`"`), &runeText) != nil {
						return out.String()
					}
					if runeText == "\ufffd" {
						// Keep incomplete UTF-16 surrogate pairs until both escapes arrive.
						var code uint16
						for _, c := range rest[i+2 : i+6] {
							code *= 16
							switch {
							case c >= '0' && c <= '9':
								code += uint16(c - '0')
							case c >= 'a' && c <= 'f':
								code += uint16(c - 'a' + 10)
							case c >= 'A' && c <= 'F':
								code += uint16(c - 'A' + 10)
							default:
								return out.String()
							}
						}
						if utf16.IsSurrogate(rune(code)) {
							n = 12
							if i+n > len(rest) {
								break
							}
						}
					}
				}
				var decoded string
				if json.Unmarshal([]byte(`"`+rest[i:i+n]+`"`), &decoded) != nil {
					return out.String()
				}
				out.WriteString(decoded)
				i += n
				continue
			}
			r, n := utf8.DecodeRuneInString(rest[i:])
			if r == utf8.RuneError && n == 1 || r < 32 {
				break
			}
			out.WriteRune(r)
			i += n
		}
		return out.String()
	}
	return ""
}

func (m *opsModel) chatStream(ctx context.Context, cfg *Config, key, prompt string, limit int, planning bool, emit func(opschat.StreamChunk) error) (string, error) {
	if len(prompt) > 32<<10 {
		return "", opschat.ErrQuota
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL(cfg.Provider)
	}
	client, cleanup, err := m.pinnedClient(ctx, base)
	if err != nil {
		return "", err
	}
	defer cleanup()
	client.Timeout = 60 * time.Second
	var endpoint string
	body := map[string]any{"model": cfg.Model, "messages": []map[string]string{{"role": "user", "content": prompt}}, "stream": true}
	switch cfg.Provider {
	case ProviderOpenAI:
		endpoint = apiEndpoint(cfg.Provider, base, "chat/completions")
		body["max_tokens"] = 4096
	case ProviderClaude:
		endpoint = apiEndpoint(cfg.Provider, base, "messages")
		body["max_tokens"] = 4096
	case ProviderOllama:
		endpoint = apiEndpoint(cfg.Provider, base, "chat")
	default:
		return "", opschat.ErrUnavailable
	}
	opsRequestCompatibility(cfg, base, body)
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", opsFailure("invalid_response", 0)
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Provider == ProviderClaude {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	if cfg.Provider == ProviderOpenAI {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", opsTransportFailure(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", opsHTTPFailure(resp.StatusCode)
	}
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	streaming := contentType == "text/event-stream" || cfg.Provider == ProviderOllama && (contentType == "application/x-ndjson" || contentType == "application/ndjson")
	if !streaming {
		// Some gateways ignore stream. Consume this one response, never retry a billable request.
		raw, err = io.ReadAll(io.LimitReader(resp.Body, opsEnvelopeBytes+1))
		if err != nil {
			return "", opsTransportFailure(ctx, err)
		}
		if len(raw) > opsEnvelopeBytes {
			return "", opsResponseLimit()
		}
		if err = opsCompletionFailure(cfg.Provider, raw); err != nil {
			return "", err
		}
		text, err := extractChatText(cfg.Provider, raw)
		if err != nil {
			return "", opsFailure("invalid_response", 0)
		}
		if len(text) > limit {
			return "", opsResponseLimit()
		}
		if strings.TrimSpace(text) == "" {
			return "", opsFailure("empty_response", 0)
		}
		visible := text
		if planning {
			visible = planTextPrefix(text)
		}
		visible = m.safeText(visible)
		if err = emit(opschat.StreamChunk{Text: visible, Fallback: true}); err != nil {
			return "", err
		}
		if planning {
			return text, nil
		}
		return visible, nil
	}
	// Bound wire bytes, individual frames, output bytes and total frames during reads.
	reader := &io.LimitedReader{R: resp.Body, N: 512<<10 + 1}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), opsEnvelopeBytes)
	maskStream := m.masker.NewStream()
	var full, safe strings.Builder
	published := 0
	frames := 0
	done := false
	finishOK := false
	publish := func(delta string) error {
		if !utf8.ValidString(delta) {
			return opsFailure("invalid_response", 0)
		}
		if full.Len()+len(delta) > limit {
			return opsResponseLimit()
		}
		full.WriteString(delta)
		visible := full.String()
		if planning {
			visible = planTextPrefix(visible)
		}
		if len(visible) < published {
			return opsFailure("invalid_response", 0)
		}
		part := maskStream.Write(visible[published:])
		published = len(visible)
		if safe.Len()+len(part) > limit {
			return opsResponseLimit()
		}
		safe.WriteString(part)
		if part != "" {
			return emit(opschat.StreamChunk{Text: part})
		}
		return nil
	}
	frame := func(data string) error {
		frames++
		if frames > 4096 {
			return opsResponseLimit()
		}
		if data == "[DONE]" {
			done = true
			return nil
		}
		var event struct {
			Type    string          `json:"type"`
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done       bool   `json:"done"`
			DoneReason string `json:"done_reason"`
		}
		if !utf8.ValidString(data) || json.Unmarshal([]byte(data), &event) != nil {
			return opsFailure("invalid_response", 0)
		}
		if len(event.Error) > 0 && string(event.Error) != "null" || event.Type == "error" {
			return opsFailure("rejected", 0)
		}
		switch cfg.Provider {
		case ProviderOpenAI:
			for _, choice := range event.Choices {
				if choice.Index != 0 {
					continue
				}
				if choice.FinishReason != nil {
					if err := opsStopFailure(*choice.FinishReason, "stop"); err != nil {
						return err
					}
					finishOK = true
				}
				if err := publish(choice.Delta.Content); err != nil {
					return err
				}
			}
		case ProviderClaude:
			if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
				if err := publish(event.Delta.Text); err != nil {
					return err
				}
			}
			if event.Type == "message_delta" && event.Delta.StopReason != "" {
				if err := opsStopFailure(event.Delta.StopReason, "end_turn", "stop_sequence"); err != nil {
					return err
				}
				finishOK = true
			}
			if event.Type == "message_stop" {
				done = true
			}
		case ProviderOllama:
			if err := publish(event.Message.Content); err != nil {
				return err
			}
			if event.Done {
				done = true
				if err := opsStopFailure(event.DoneReason, "", "stop"); err != nil {
					return err
				}
				finishOK = true
			}
		}
		return nil
	}
	data := ""
	for scanner.Scan() {
		if ctx.Err() != nil {
			return "", opsTransportFailure(ctx, ctx.Err())
		}
		line := scanner.Text()
		if cfg.Provider == ProviderOllama {
			if err = frame(line); err != nil {
				return "", err
			}
		} else {
			if line == "" && data != "" {
				err = frame(strings.TrimSuffix(data, "\n"))
				data = ""
				if err != nil {
					return "", err
				}
			} else if strings.HasPrefix(line, "data:") {
				data += strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ") + "\n"
				if len(data) > opsEnvelopeBytes {
					return "", opsResponseLimit()
				}
			}
		}
		if done {
			break
		}
	}
	if !done && data != "" {
		if err = frame(strings.TrimSuffix(data, "\n")); err != nil {
			return "", err
		}
	}
	if ctx.Err() != nil {
		return "", opsTransportFailure(ctx, ctx.Err())
	}
	if reader.N <= 0 || errors.Is(scanner.Err(), bufio.ErrTooLong) {
		return "", opsResponseLimit()
	}
	if scanner.Err() != nil {
		return "", opsTransportFailure(ctx, scanner.Err())
	}
	if !done || !finishOK {
		return "", opsFailure("incomplete_stream", 0)
	}
	if strings.TrimSpace(full.String()) == "" {
		return "", opsFailure("empty_response", 0)
	}
	part := maskStream.Finish()
	if safe.Len()+len(part) > limit {
		return "", opsResponseLimit()
	}
	safe.WriteString(part)
	if part != "" {
		if err = emit(opschat.StreamChunk{Text: part}); err != nil {
			return "", err
		}
	}
	if planning {
		return full.String(), nil
	}
	return safe.String(), nil
}
