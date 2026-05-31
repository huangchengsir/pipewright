package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/qualitygate"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/testreport"
)

// dag_test_report.go 实现「测试报告采集 + 质量门禁」(Epic 8 · Story 8-6 / FR-8-6)。
//
// 采集模型(复用已有挂载,不另造):script 步骤把测试报告写进克隆工作区(容器 /workspace
// 挂载的就是宿主 workspace 目录),容器退出后报告文件即在宿主 workspace/<reportPath> 可读。
// 阶段所有 script job 成功执行后,本层据该阶段任一 job 声明的报告配置读文件 → 解析 → 持久化
// 汇总 → 据门禁阈值裁决。门禁不过 → 返回 ErrQualityGate,令阶段失败,从而**阻断下游部署阶段**
// (复用 dagrun「阶段失败 → 下游不执行」语义,零新机制)。
//
// 门禁原因串只含计数/阈值数字,不含报告原文(无 secret 外泄)。报告文件不存在但声明了门禁 →
// 阻断(声明了要求却拿不到证据,宁阻断不放过);仅声明展示(无门禁)时文件缺失 → 跳过不报错。

// ErrQualityGate 表示质量门禁未通过(该阶段失败 → 运行终止,下游部署不执行)。
var ErrQualityGate = errors.New("build: quality gate not satisfied")

// TestReportSink 抽象「持久化一条测试报告汇总」的能力(由 run.Service 实现;测试可注入 fake)。
// 解耦 internal/build 对 run.Service 全表面的依赖。
type TestReportSink interface {
	SaveTestReport(ctx context.Context, tr run.TestReport) (*run.TestReport, error)
}

// job.Config 报告/门禁键(对齐前端 script 节点表单;均为可选字符串值):
//
//	testReport      报告格式开关:值为 "junit" 时启用测试报告采集(其它/缺省 = 不采集)。
//	reportPath      JUnit XML 报告路径(相对工作区根;testReport=junit 时必填)。
//	coverageReport  覆盖率格式:值为 "cobertura" 时启用覆盖率采集(可选)。
//	coveragePath    Cobertura XML 覆盖率路径(相对工作区根;coverageReport=cobertura 时读)。
//	gateMaxFailures 门禁:允许的最大失败用例数(整数;缺省/空 = 不检查)。
//	gateMinCoverage 门禁:要求的最小行覆盖率%(数字;缺省/空 = 不检查)。
const (
	cfgTestReport      = "testReport"
	cfgReportPath      = "reportPath"
	cfgCoverageReport  = "coverageReport"
	cfgCoveragePath    = "coveragePath"
	cfgGateMaxFailures = "gateMaxFailures"
	cfgGateMinCoverage = "gateMinCoverage"
)

// reportSpec 是从一个阶段的 job 配置里抽取的报告采集声明。
type reportSpec struct {
	reportPath   string
	coveragePath string // 为空 = 不采集覆盖率
	thresholds   qualitygate.Thresholds
}

// reportSpecFromStage 从阶段的 script job 里找出第一个声明了 testReport=junit 的报告配置。
// 无声明 → (nil, nil)(该阶段不采集报告)。
func reportSpecFromStage(stage pipeline.Stage) *reportSpec {
	for _, jb := range stage.Jobs {
		if !strings.EqualFold(cfgString(jb.Config, cfgTestReport), "junit") {
			continue
		}
		rp := cfgString(jb.Config, cfgReportPath)
		if rp == "" {
			continue
		}
		spec := &reportSpec{
			reportPath: rp,
			thresholds: qualitygate.Thresholds{
				MaxFailures: cfgIntDefault(jb.Config, cfgGateMaxFailures, qualitygate.NoCheck),
				MinCoverage: cfgFloatDefault(jb.Config, cfgGateMinCoverage, float64(qualitygate.NoCheck)),
			},
		}
		if strings.EqualFold(cfgString(jb.Config, cfgCoverageReport), "cobertura") {
			spec.coveragePath = cfgString(jb.Config, cfgCoveragePath)
		}
		return spec
	}
	return nil
}

// collectStageReport 在阶段 script job 成功执行后采集/解析/持久化测试报告并裁决门禁。
//
// 返回值:门禁不过 → ErrQualityGate(调用方据此令阶段失败);其余错误(配置缺失文件 + 声明了门禁)
// 同样阻断。仅展示(无门禁)时解析失败/文件缺失 → 仅记日志,返回 nil(不阻断)。
// sink 为 nil(未注入持久层)时:仍解析 + 评估门禁(阻断仍生效),只是不落库。
func collectStageReport(ctx context.Context, sink TestReportSink, r *run.Run, stage pipeline.Stage, workspace string, rep dagrun.StageReporter) error {
	spec := reportSpecFromStage(stage)
	if spec == nil {
		return nil // 该阶段未声明测试报告
	}

	gated := spec.thresholds.Enabled()

	// 读 + 解析 JUnit。
	xmlPath := safeJoinWorkspace(workspace, spec.reportPath)
	data, rerr := os.ReadFile(xmlPath)
	if rerr != nil {
		msg := fmt.Sprintf("测试报告文件未找到或不可读:%s", spec.reportPath)
		_ = rep.Log(ctx, streamStderr, msg)
		if gated {
			_ = rep.Log(ctx, streamStderr, "⛔ 已声明质量门禁但缺测试报告,阻断后续阶段")
			return ErrQualityGate
		}
		return nil // 仅展示:软失败
	}
	summary, perr := testreport.ParseJUnit(data)
	if perr != nil {
		_ = rep.Log(ctx, streamStderr, fmt.Sprintf("测试报告解析失败:%v", perr))
		if gated {
			_ = rep.Log(ctx, streamStderr, "⛔ 已声明质量门禁但报告无法解析,阻断后续阶段")
			return ErrQualityGate
		}
		return nil
	}

	// 可选覆盖率(次要项;失败软处理)。
	if spec.coveragePath != "" {
		covPath := safeJoinWorkspace(workspace, spec.coveragePath)
		if cdata, cerr := os.ReadFile(covPath); cerr == nil {
			if pct, cperr := testreport.ParseCoberturaCoverage(cdata); cperr == nil {
				summary.Coverage = pct
			} else {
				_ = rep.Log(ctx, streamStdout, "覆盖率报告无法解析,按未提供覆盖率处理")
			}
		} else {
			_ = rep.Log(ctx, streamStdout, "覆盖率报告未找到,按未提供覆盖率处理")
		}
	}

	// 门禁裁决。
	verdict := qualitygate.Evaluate(summary, spec.thresholds)

	// 报告概要日志(无 secret;仅数字)。
	covText := "n/a"
	if summary.Coverage != testreport.CoverageUnknown {
		covText = fmt.Sprintf("%.1f%%", summary.Coverage)
	}
	_ = rep.Log(ctx, streamStdout, fmt.Sprintf(
		"📊 测试报告:通过 %d · 失败 %d · 跳过 %d(共 %d)· 覆盖率 %s",
		summary.Passed, summary.Failed, summary.Skipped, summary.Total, covText))

	// 持久化汇总(best-effort:落库失败不改变门禁裁决;仅记日志)。
	if sink != nil {
		tr := run.TestReport{
			RunID:       r.ID,
			StageID:     stage.ID,
			StageName:   stage.Name,
			Format:      "junit",
			Total:       summary.Total,
			Passed:      summary.Passed,
			Failed:      summary.Failed,
			Skipped:     summary.Skipped,
			DurationSec: summary.DurationSeconds,
			Coverage:    summary.Coverage,
			GateEnabled: gated,
			GatePassed:  verdict.Passed,
			GateReason:  verdict.Reason(),
		}
		if _, serr := sink.SaveTestReport(ctx, tr); serr != nil {
			_ = rep.Log(ctx, streamStderr, "测试报告汇总持久化失败(不影响门禁裁决)")
		}
	}

	if gated && !verdict.Passed {
		_ = rep.Log(ctx, streamStderr, "⛔ 质量门禁未通过:"+verdict.Reason())
		_ = rep.Log(ctx, streamStderr, "已阻断后续阶段(含部署)")
		return ErrQualityGate
	}
	if gated {
		_ = rep.Log(ctx, streamStdout, "✅ 质量门禁通过")
	}
	return nil
}

// safeJoinWorkspace 把工作区根与用户给的相对报告路径拼成宿主绝对路径,清洗 `.`/`..`/前导斜杠
// 防穿越逃出工作区(读文件前的纵深防御)。
func safeJoinWorkspace(workspace, rel string) string {
	parts := []string{}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		parts = append(parts, seg)
	}
	return filepath.Join(append([]string{workspace}, parts...)...)
}

// cfgIntDefault 从 config 取整数值(字符串/数字皆容;缺失/非法 → def)。
func cfgIntDefault(cfg map[string]any, key string, def int) int {
	if cfg == nil {
		return def
	}
	switch v := cfg[key].(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return def
		}
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

// cfgFloatDefault 从 config 取浮点值(字符串/数字皆容;缺失/非法 → def)。
func cfgFloatDefault(cfg map[string]any, key string, def float64) float64 {
	if cfg == nil {
		return def
	}
	switch v := cfg[key].(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return def
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	case float64:
		return v
	case int:
		return float64(v)
	}
	return def
}
