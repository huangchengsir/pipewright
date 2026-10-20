package httpapi

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/anomaly"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/metrics"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type anomalyAnalyzer struct {
	ai      ai.Service
	servers target.Service
	vault   vault.Vault
	history metrics.Service
	masker  *mask.Masker
}

func NewAnomalyAnalyzer(model ai.Service, servers target.Service, credentials vault.Vault, history metrics.Service, masker *mask.Masker) anomaly.Analyzer {
	if masker == nil {
		masker = mask.NewMasker()
	}
	return &anomalyAnalyzer{ai: model, servers: servers, vault: credentials, history: history, masker: masker}
}

func analysisUnavailable(reason string) *anomaly.Analysis {
	return &anomaly.Analysis{Status: anomaly.AnalysisUnavailable, Reason: reason}
}

func (a *anomalyAnalyzer) AnalyzeAlert(ctx context.Context, in anomaly.AnalysisInput) (*anomaly.Analysis, error) {
	if a.ai == nil {
		return analysisUnavailable("AI 未配置或未启用"), nil
	}
	cfg, err := a.ai.Get(ctx)
	if err != nil || cfg == nil || !cfg.Configured || !cfg.Enabled {
		return analysisUnavailable("AI 未配置或未启用"), nil
	}
	if registrar, ok := ai.NewOpsModel(a.ai, a.masker).(interface{ RegisterSecrets(context.Context) error }); ok {
		if registrar.RegisterSecrets(ctx) != nil {
			return analysisUnavailable("诊断数据脱敏不可用"), nil
		}
	}
	// Register known secrets before any fixed diagnostic output can become model input.
	if a.vault == nil {
		return analysisUnavailable("诊断数据脱敏不可用"), nil
	}
	if err := vault.RegisterMaskSecrets(ctx, a.vault, a.masker); err != nil {
		return analysisUnavailable("诊断数据脱敏不可用"), nil
	}
	var facts []string
	add := func(value any) {
		body, err := json.Marshal(value)
		if err == nil && len(facts) < 32 {
			facts = append(facts, a.masker.ScrubTruncated(string(body)))
		}
	}
	add(map[string]any{"kind": "alert", "metric": in.Alert.Metric, "operator": in.Alert.Operator, "threshold": in.Alert.Threshold, "value": in.Alert.Value, "at": in.Alert.CreatedAt.UTC().Format(time.RFC3339)})
	add(map[string]any{"kind": "metrics", "cpuLoadRatioPercent": in.Metrics.CPUPercent, "memoryPercent": in.Metrics.MemoryPercent, "diskPercent": in.Metrics.DiskPercent})
	if a.history != nil {
		points, err := a.history.QueryRange(ctx, in.Alert.ServerID, in.Alert.CreatedAt.Add(-15*time.Minute))
		if err == nil {
			eligible := points[:0]
			for _, point := range points {
				if !point.At.After(in.Alert.CreatedAt) {
					eligible = append(eligible, point)
				}
			}
			if len(eligible) > 12 {
				eligible = eligible[len(eligible)-12:]
			}
			for _, point := range eligible {
				add(map[string]any{"kind": "history", "at": point.At.UTC().Format(time.RFC3339), "cpuLoadRatioPercent": point.CPU, "memoryPercent": point.Memory, "diskPercent": point.Disk})
			}
		} else {
			add(map[string]any{"kind": "history", "unavailable": true})
		}
	}
	executor, bounded := a.servers.(target.LimitedExecutor)
	if bounded {
		snapshot, err := executor.Capture(ctx, in.Alert.ServerID)
		if err == nil {
			if in.Alert.Metric == anomaly.MetricCPU || in.Alert.Metric == anomaly.MetricMemory {
				sort := "--sort=-pcpu"
				if in.Alert.Metric == anomaly.MetricMemory {
					sort = "--sort=-pmem"
				}
				result := analysisCommand(ctx, executor, snapshot, []string{"env", "LC_ALL=C", "ps", "-eo", "pid,comm,pcpu,pmem", "--no-headers", sort})
				if result == nil {
					add(map[string]any{"kind": "processes", "unavailable": true})
				} else {
					observed, incomplete := analysisProcesses(result.Stdout, a.masker)
					add(map[string]any{"kind": "processes", "items": observed, "unavailable": len(observed) == 0, "truncated": result.Truncated || incomplete, "scope": "top 20 names and resource percentages; no command arguments"})
				}
			} else {
				add(map[string]any{"kind": "diskOwners", "unavailable": true, "reason": "directory and volume sizes were not collected"})
			}
			result := analysisCommand(ctx, executor, snapshot, []string{"docker", "stats", "--no-stream", "--format", `{"name":{{json .Name}},"cpu":{{json .CPUPerc}},"memory":{{json .MemUsage}}}`})
			if result == nil {
				add(map[string]any{"kind": "containers", "unavailable": true})
			} else {
				observed, incomplete := analysisContainers(result.Stdout, a.masker)
				add(map[string]any{"kind": "containers", "items": observed, "truncated": result.Truncated || incomplete, "scope": "running container resource usage, not stopped containers or volume sizes"})
			}
		} else {
			add(map[string]any{"kind": "additionalCollection", "unavailable": true})
		}
	} else {
		add(map[string]any{"kind": "additionalCollection", "unavailable": true})
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	data := strings.Join(facts, "\n")
	if len(data) > 7500 {
		return analysisUnavailable("诊断数据超过安全长度限制"), nil
	}
	diagnosis, err := a.ai.Diagnose(ctx, ai.DiagnoseInput{FailureLog: data, Monitoring: true, Masker: a.masker})
	if err != nil || diagnosis == nil {
		return analysisUnavailable("AI 分析失败"), nil
	}
	out := &anomaly.Analysis{Status: diagnosis.Status, Reason: a.masker.ScrubTruncated(diagnosis.Reason), Hypothesis: a.masker.ScrubTruncated(diagnosis.Hypothesis), Confidence: diagnosis.Confidence, AlternateCauses: []string{}, FixSuggestions: []string{}, Evidence: []anomaly.Evidence{}}
	for _, value := range diagnosis.AlternateCauses {
		out.AlternateCauses = append(out.AlternateCauses, a.masker.ScrubTruncated(value))
	}
	for _, value := range diagnosis.FixSuggestions {
		out.FixSuggestions = append(out.FixSuggestions, a.masker.ScrubTruncated(value))
	}
	for _, evidence := range diagnosis.Evidence {
		if evidence.Line > 0 && evidence.Line <= len(facts) {
			out.Evidence = append(out.Evidence, anomaly.Evidence{Line: evidence.Line, Text: a.masker.ScrubTruncated(facts[evidence.Line-1])})
		}
	}
	return out, nil
}

func analysisCommand(ctx context.Context, executor target.LimitedExecutor, snapshot target.ConnectionSnapshot, argv []string) *target.LimitedResult {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	result, err := executor.ExecLimited(ctx, snapshot, argv, target.ExecutionLimits{Bytes: 8 << 10, Lines: 26})
	if err != nil || result == nil || result.ExitCode != 0 {
		return nil
	}
	return result
}

type analysisProcess struct {
	Name   string  `json:"name"`
	CPU    float64 `json:"cpuPercent"`
	Memory float64 `json:"memoryPercent"`
}

func analysisProcesses(raw string, masker *mask.Masker) ([]analysisProcess, bool) {
	items := []analysisProcess{}
	incomplete := false
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			incomplete = true
			continue
		}
		_, idErr := strconv.ParseUint(fields[0], 10, 64)
		cpu, cpuErr := strconv.ParseFloat(fields[2], 64)
		memory, memErr := strconv.ParseFloat(fields[3], 64)
		if idErr != nil || cpuErr != nil || memErr != nil || cpu < 0 || memory < 0 || memory > 100 || math.IsNaN(cpu) || math.IsInf(cpu, 0) || math.IsNaN(memory) || math.IsInf(memory, 0) {
			incomplete = true
			continue
		}
		if len(items) == 20 {
			incomplete = true
			break
		}
		name := masker.ScrubTruncated(fields[1])
		if len(name) > 128 {
			incomplete = true
			continue
		}
		items = append(items, analysisProcess{Name: name, CPU: cpu, Memory: memory})
	}
	return items, incomplete
}

type analysisContainer struct {
	Name   string `json:"name"`
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

func analysisContainers(raw string, masker *mask.Masker) ([]analysisContainer, bool) {
	items := []analysisContainer{}
	incomplete := false
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var value analysisContainer
		if json.Unmarshal([]byte(line), &value) != nil || value.Name == "" || len(value.Name) > 128 || len(value.CPU) > 32 || len(value.Memory) > 96 {
			incomplete = true
			continue
		}
		if len(items) == 12 {
			incomplete = true
			break
		}
		value.Name = masker.ScrubTruncated(value.Name)
		value.CPU = masker.ScrubTruncated(value.CPU)
		value.Memory = masker.ScrubTruncated(value.Memory)
		items = append(items, value)
	}
	return items, incomplete
}
