package anomaly

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"log"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	AnalysisPending            = "pending"
	AnalysisReady              = "ready"
	AnalysisUnavailable        = "unavailable"
	alertColumns               = "id, server_id, server_name, metric, operator, threshold, value, message, created_at, rule_id, ai_analysis_enabled, analysis_status, analysis_json"
	analysisPersistenceTimeout = 2 * time.Second
	analysisRecoveryBatchSize  = 16
	analysisRecoveryInterval   = 100 * time.Millisecond
	notifierDrainTimeout       = 12 * time.Second
)

type Evidence struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

type Analysis struct {
	Status          string     `json:"status"`
	Reason          string     `json:"reason"`
	Hypothesis      string     `json:"hypothesis"`
	Confidence      string     `json:"confidence"`
	AlternateCauses []string   `json:"alternateCauses"`
	FixSuggestions  []string   `json:"fixSuggestions"`
	Evidence        []Evidence `json:"evidence"`
	GeneratedAt     string     `json:"generatedAt"`
}

type AnalysisInput struct {
	Alert   Alert
	Metrics ServerMetricsSnapshot
}

type Analyzer interface {
	AnalyzeAlert(context.Context, AnalysisInput) (*Analysis, error)
}

type analysisJob struct {
	ruleID string
	cancel context.CancelFunc
}

func WithAnalyzer(a Analyzer) Option { return func(s *service) { s.analyzer = a } }

func WithAnalysisTimeout(d time.Duration) Option {
	return func(s *service) {
		if d > 0 && d <= 30*time.Second {
			s.analysisTimeout = d
		}
	}
}

func boolValue(v *bool) int {
	if v != nil && *v {
		return 1
	}
	return 0
}

func optionalBoolValue(v *bool) any {
	if v == nil {
		return nil
	}
	return boolValue(v)
}

func cloneMetrics(in ServerMetricsSnapshot) ServerMetricsSnapshot {
	copyValue := func(p *float64) *float64 {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	in.CPUPercent = copyValue(in.CPUPercent)
	in.MemoryPercent = copyValue(in.MemoryPercent)
	in.DiskPercent = copyValue(in.DiskPercent)
	return in
}

func (s *service) Close() {
	s.analysisMu.Lock()
	s.closed = true
	s.analysisCancel()
	s.analysisMu.Unlock()
	s.checkWG.Wait()
	s.analysisWG.Wait()
	s.recoveryWG.Wait()
	if notifier, ok := s.notifier.(interface{ Drain(context.Context) error }); ok {
		ctx, cancel := context.WithTimeout(context.Background(), notifierDrainTimeout)
		defer cancel()
		if err := notifier.Drain(ctx); err != nil {
			log.Print("[anomaly] notification drain interrupted")
		}
	}
}

func (s *service) cancelRuleAnalysis(id string) {
	s.analysisMu.Lock()
	defer s.analysisMu.Unlock()
	for _, job := range s.analysisJobs {
		if job.ruleID == id {
			job.cancel()
		}
	}
}

func (s *service) dispatchAlert(ctx context.Context, alert Alert, snapshot ServerMetricsSnapshot) {
	if !alert.AIAnalysisEnabled {
		s.notifyAlert(ctx, &alert)
		return
	}
	if s.analyzer == nil {
		s.finishAnalysis(alert, unavailableAnalysis("AI 分析未配置，原始告警照常发送"))
		return
	}
	select {
	case s.analysisSlots <- struct{}{}:
	default:
		s.finishAnalysis(alert, unavailableAnalysis("AI 分析繁忙，原始告警照常发送"))
		return
	}
	jobCtx, cancel := context.WithTimeout(s.analysisContext, s.analysisTimeout)
	s.analysisMu.Lock()
	if s.closed {
		s.analysisMu.Unlock()
		cancel()
		<-s.analysisSlots
		s.finishAnalysis(alert, unavailableAnalysis("AI 分析已中断，原始告警照常发送"))
		return
	}
	s.analysisJobs[alert.ID] = analysisJob{ruleID: alert.RuleID, cancel: cancel}
	s.analysisWG.Add(1)
	s.analysisMu.Unlock()
	go s.analyzeAlert(jobCtx, alert, snapshot, cancel)
}

func (s *service) analyzeAlert(ctx context.Context, alert Alert, snapshot ServerMetricsSnapshot, cancel context.CancelFunc) {
	var result *Analysis
	defer func() {
		cancel()
		<-s.analysisSlots
		s.analysisMu.Lock()
		delete(s.analysisJobs, alert.ID)
		s.analysisMu.Unlock()
		s.analysisWG.Done()
	}()
	release, admissionErr := maintenance.Enter(ctx, s.db, "alert_analysis")
	if admissionErr != nil {
		s.finishAnalysis(alert, unavailableAnalysis("AI 分析不可用，原始告警照常发送"))
		return
	}
	defer release()
	defer func() {
		if recover() != nil {
			result = unavailableAnalysis("AI 分析失败，原始告警照常发送")
		}
		if ctx.Err() != nil {
			reason := "AI 分析超时，原始告警照常发送"
			if ctx.Err() == context.Canceled {
				reason = "AI 分析已中断，原始告警照常发送"
			}
			result = unavailableAnalysis(reason)
		}
		if result == nil {
			result = unavailableAnalysis("AI 分析不可用，原始告警照常发送")
		}
		s.finishAnalysis(alert, result)
	}()
	// Re-read the rule after registering cancellation, so disabling/deleting it cannot start stale work.
	rule, err := s.getRule(ctx, alert.RuleID)
	if err != nil || !rule.Enabled || !rule.AIAnalysisEnabled {
		result = unavailableAnalysis("告警规则已关闭 AI 分析，原始告警照常发送")
		return
	}
	result, err = s.analyzer.AnalyzeAlert(ctx, AnalysisInput{Alert: alert, Metrics: snapshot})
	if err != nil {
		result = unavailableAnalysis("AI 分析失败，原始告警照常发送")
	}
}

func unavailableAnalysis(reason string) *Analysis {
	return &Analysis{Status: AnalysisUnavailable, Reason: reason}
}

func limitText(value string, max int) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "")
	if len(value) <= max {
		return value
	}
	value = value[:max]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func boundedList(values []string, count, bytes int) []string {
	out := []string{}
	for _, v := range values {
		if len(out) >= count {
			break
		}
		if v = limitText(v, bytes); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func normalizeAnalysis(in *Analysis) *Analysis {
	if in == nil || (in.Status != AnalysisReady && in.Status != AnalysisUnavailable) {
		in = unavailableAnalysis("AI 返回内容无效，原始告警照常发送")
	}
	out := &Analysis{Status: in.Status, GeneratedAt: time.Now().UTC().Format(time.RFC3339), AlternateCauses: []string{}, FixSuggestions: []string{}, Evidence: []Evidence{}}
	if in.Status == AnalysisUnavailable {
		out.Reason = limitText(in.Reason, 512)
		if out.Reason == "" {
			out.Reason = "AI 分析不可用，原始告警照常发送"
		}
		return out
	}
	out.Hypothesis = limitText(in.Hypothesis, 2048)
	if out.Hypothesis == "" {
		return normalizeAnalysis(unavailableAnalysis("AI 未提供有效原因，原始告警照常发送"))
	}
	switch in.Confidence {
	case "high", "medium", "low":
		out.Confidence = in.Confidence
	default:
		out.Confidence = "low"
	}
	out.AlternateCauses = boundedList(in.AlternateCauses, 4, 512)
	out.FixSuggestions = boundedList(in.FixSuggestions, 5, 640)
	for _, e := range in.Evidence {
		if len(out.Evidence) == 6 {
			break
		}
		if e.Line > 0 && strings.TrimSpace(e.Text) != "" {
			out.Evidence = append(out.Evidence, Evidence{Line: e.Line, Text: limitText(e.Text, 512)})
		}
	}
	return out
}

func (s *service) finishAnalysis(alert Alert, analysis *Analysis) {
	ctx, cancel := context.WithTimeout(context.Background(), analysisPersistenceTimeout)
	defer cancel()
	if !s.persistAnalysis(ctx, alert, analysis) {
		log.Print("[anomaly] analysis persistence failed; pending alert retained for local recovery")
		s.scheduleAnalysisRecovery()
	}
}

// A successful terminal CAS is required before best-effort notification; recovery never calls the model.
func (s *service) persistAnalysis(ctx context.Context, alert Alert, analysis *Analysis) bool {
	alert.Analysis = normalizeAnalysis(analysis)
	body, err := json.Marshal(alert.Analysis)
	if err != nil {
		return false
	}
	res, err := s.db.ExecContext(ctx, `UPDATE anomaly_alerts SET analysis_status = ?, analysis_json = ? WHERE id = ? AND analysis_status = ?`, alert.Analysis.Status, string(body), alert.ID, AnalysisPending)
	if err != nil {
		return false
	}
	count, err := res.RowsAffected()
	if err != nil {
		return false
	}
	if count == 0 {
		return true
	}
	s.notifyAlert(ctx, &alert)
	return true
}

func (s *service) notifyAlert(ctx context.Context, alert *Alert) {
	defer func() {
		if recover() != nil {
			log.Print("[anomaly] notification failed")
		}
	}()
	if s.notifier != nil {
		s.notifier.NotifyAlert(ctx, alert)
	}
}

func decodeAnalysis(status, body string) *Analysis {
	if status == "" {
		return nil
	}
	if status == AnalysisPending {
		return &Analysis{Status: AnalysisPending}
	}
	var result Analysis
	if json.Unmarshal([]byte(body), &result) != nil {
		return normalizeAnalysis(unavailableAnalysis("AI 分析结果不可读，原始告警记录已保留"))
	}
	result.Status = status
	out := normalizeAnalysis(&result)
	if _, err := time.Parse(time.RFC3339, result.GeneratedAt); err == nil {
		out.GeneratedAt = result.GeneratedAt
	}
	return out
}

func (s *service) scheduleAnalysisRecovery() {
	s.analysisMu.Lock()
	defer s.analysisMu.Unlock()
	if s.closed {
		return
	}
	s.recoveryGeneration++
	if s.recoveryRunning {
		return
	}
	s.recoveryRunning = true
	s.recoveryWG.Add(1)
	go s.analysisRecoveryLoop()
}

func (s *service) analysisRecoveryLoop() {
	defer s.recoveryWG.Done()
	for {
		timer := time.NewTimer(analysisRecoveryInterval)
		select {
		case <-timer.C:
		case <-s.analysisContext.Done():
			timer.Stop()
		}
		s.analysisMu.Lock()
		generation := s.recoveryGeneration
		s.analysisMu.Unlock()
		pending := s.recoverAnalysis()
		s.analysisMu.Lock()
		if s.closed || (!pending && generation == s.recoveryGeneration) {
			s.recoveryRunning = false
			s.analysisMu.Unlock()
			return
		}
		s.analysisMu.Unlock()
	}
}

// Each sweep has one deadline and bounded pages. The check gate protects newly recorded alerts
// until dispatch has registered their analysis jobs; active model calls are never recovered.
func (s *service) recoverAnalysis() bool {
	ctx, cancel := context.WithTimeout(s.analysisContext, analysisPersistenceTimeout)
	defer cancel()
	select {
	case s.checkGate <- struct{}{}:
		defer func() { <-s.checkGate }()
	case <-ctx.Done():
		return true
	}
	pending := false
	afterID := ""
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT `+alertColumns+` FROM anomaly_alerts WHERE analysis_status = ? AND id > ? ORDER BY id LIMIT ?`, AnalysisPending, afterID, analysisRecoveryBatchSize)
		if err != nil {
			return true
		}
		batch := make([]Alert, 0, analysisRecoveryBatchSize)
		for rows.Next() {
			a, err := scanAlert(rows)
			if err != nil {
				rows.Close()
				return true
			}
			batch = append(batch, *a)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return true
		}
		if len(batch) == 0 {
			return pending
		}
		for _, a := range batch {
			afterID = a.ID
			s.analysisMu.Lock()
			_, active := s.analysisJobs[a.ID]
			s.analysisMu.Unlock()
			if active {
				pending = true
				continue
			}
			if !s.persistAnalysis(ctx, a, unavailableAnalysis("AI 分析已中断或结果未能保存，原始告警照常发送")) {
				pending = true
			}
			if ctx.Err() != nil {
				return true
			}
		}
	}
}

func AnalysisText(a *Analysis) string {
	if a == nil || a.Status == AnalysisPending {
		return ""
	}
	if a.Status != AnalysisReady {
		return "AI 分析不可用：" + limitText(a.Reason, 512)
	}
	var b strings.Builder
	b.WriteString("AI 分析（假说，非结论）\n" + limitText(a.Hypothesis, 800))
	if len(a.FixSuggestions) > 0 {
		b.WriteString("\n处理建议：")
		for _, suggestion := range boundedList(a.FixSuggestions, 4, 320) {
			fmt.Fprintf(&b, "\n- %s", suggestion)
		}
	}
	if len(a.Evidence) > 0 {
		b.WriteString("\n证据：")
		for i, e := range a.Evidence {
			if i == 2 {
				break
			}
			fmt.Fprintf(&b, "\n- %s", limitText(e.Text, 200))
		}
	}
	return limitText(b.String(), 2600)
}
