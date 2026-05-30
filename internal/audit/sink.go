package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// envAuditSink 是远端 sink 配置环境变量(AC-SEC-03)。
//
// 取值形式:
//   - "http://host/path" / "https://host/path" → HTTP append sink(POST JSON 每条)
//   - 其它非空值视为本地第二文件路径(file:// 前缀可选)→ JSON Lines append
//
// 空/未设 → 无远端 sink(仅本地 append-only 表)。
const envAuditSink = "DEVOPSTOOL_AUDIT_SINK"

// sinkRecordDTO 是 sink 推送的脱敏审计记录线格式(已脱敏;绝无明文 secret)。
type sinkRecordDTO struct {
	ID         string         `json:"id"`
	Timestamp  string         `json:"timestamp"`
	Actor      string         `json:"actor"`
	Action     string         `json:"action"`
	TargetType string         `json:"targetType"`
	TargetID   string         `json:"targetId"`
	Detail     map[string]any `json:"detail"`
	IP         string         `json:"ip"`
}

func toSinkDTO(r Record) sinkRecordDTO {
	return sinkRecordDTO{
		ID:         r.ID,
		Timestamp:  r.Timestamp.UTC().Format(time.RFC3339Nano),
		Actor:      r.Actor,
		Action:     r.Action,
		TargetType: r.TargetType,
		TargetID:   r.TargetID,
		Detail:     r.Detail,
		IP:         r.IP,
	}
}

// SinkFromEnv 按 DEVOPSTOOL_AUDIT_SINK 构造远端 sink;未配置返回 (nil, nil)。
// 不做网络/磁盘探测(无 init 副作用,避免抬高空载内存);失败延迟到 Send 时降级。
func SinkFromEnv() (Sink, error) {
	raw := strings.TrimSpace(os.Getenv(envAuditSink))
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return NewHTTPSink(raw), nil
	}
	path := strings.TrimPrefix(raw, "file://")
	return NewFileSink(path), nil
}

// FileSink 是本地第二文件 sink:每条审计以 JSON Lines 追加。与主 append-only 表
// 物理分离,删主表/主 DB 后该文件仍完整(AC-SEC-03)。并发追加以 mutex 串行化。
type FileSink struct {
	mu   sync.Mutex
	path string
}

// NewFileSink 构造文件 sink(不在此创建/打开文件;延迟到首次 Send)。
func NewFileSink(path string) *FileSink {
	return &FileSink{path: path}
}

// Send 以 JSON Lines 追加一条已脱敏记录到文件。
func (s *FileSink) Send(_ context.Context, r Record) error {
	line, err := json.Marshal(toSinkDTO(r))
	if err != nil {
		return fmt.Errorf("audit sink: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit sink: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("audit sink: write: %w", err)
	}
	return nil
}

// HTTPSink 是远端 HTTP append sink:每条审计 POST 一条 JSON。超时短(不阻断主操作)。
type HTTPSink struct {
	url    string
	client *http.Client
}

// NewHTTPSink 构造 HTTP sink(短超时;失败由 Recorder 降级吞掉)。
func NewHTTPSink(url string) *HTTPSink {
	return &HTTPSink{
		url:    url,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Send POST 一条已脱敏记录到远端。
func (s *HTTPSink) Send(ctx context.Context, r Record) error {
	body, err := json.Marshal(toSinkDTO(r))
	if err != nil {
		return fmt.Errorf("audit sink: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("audit sink: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("audit sink: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("audit sink: remote status %d", resp.StatusCode)
	}
	return nil
}
