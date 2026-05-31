<!--
  TestReportPanel.vue — Epic 8 · Story 8-6: 测试报告 + 质量门禁(FR-8-6)

  展示一次运行各阶段产出的测试报告:通过/失败/跳过计数 + 覆盖率条 + 门禁裁决。
  script 步骤声明 testReport=junit + reportPath 后,后端解析 JUnit XML(可选 Cobertura 覆盖率),
  并据 gateMaxFailures / gateMinCoverage 阈值裁决是否阻断下游部署。

  自取数据(container):测试报告不在冻结 run-detail DTO 内,经 GET /api/runs/{id}/test-report 拉取。
  无报告 → 不渲染(返回 null),不占视觉。门禁阻断时以醒目状态条标注原因(无 secret,仅数字)。
-->
<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { getRunTestReport } from '../../api/runs'
import type { TestReportDTO, GateVerdict, RunTestReportResponse } from '../../api/runs'
import { HttpError } from '../../api/http'

const props = defineProps<{
  runId: string
}>()

const reports = ref<TestReportDTO[]>([])
const gate = ref<GateVerdict>({ enabled: false, passed: true, reasons: [] })
const loaded = ref(false)
const error = ref<string | null>(null)

async function load(id: string): Promise<void> {
  loaded.value = false
  error.value = null
  try {
    const res: RunTestReportResponse = await getRunTestReport(id)
    reports.value = res.reports
    gate.value = res.gate
  } catch (err: unknown) {
    // 404 / 未就绪 → 静默(无报告等价空);其它错误给一行提示。
    if (err instanceof HttpError && err.status === 404) {
      reports.value = []
      gate.value = { enabled: false, passed: true, reasons: [] }
    } else {
      error.value = '测试报告加载失败'
    }
  } finally {
    loaded.value = true
  }
}

watch(() => props.runId, (id) => { if (id) void load(id) }, { immediate: true })

const hasReports = computed(() => reports.value.length > 0)

// 跨阶段聚合(总览条):合计通过/失败/跳过 + 覆盖率取「最低的有效值」(最保守视图)。
const totals = computed(() => {
  let passed = 0, failed = 0, skipped = 0, total = 0
  let minCoverage = -1
  for (const r of reports.value) {
    passed += r.passed
    failed += r.failed
    skipped += r.skipped
    total += r.total
    if (r.coverage >= 0) {
      minCoverage = minCoverage < 0 ? r.coverage : Math.min(minCoverage, r.coverage)
    }
  }
  return { passed, failed, skipped, total, coverage: minCoverage }
})

function coverageText(pct: number): string {
  return pct < 0 ? 'n/a' : `${pct.toFixed(1)}%`
}

function passRate(r: { passed: number; total: number }): number {
  if (r.total <= 0) return 0
  return Math.round((r.passed / r.total) * 100)
}
</script>

<template>
  <section
    v-if="loaded && (hasReports || error)"
    class="test-report"
    :class="{ 'test-report--blocked': gate.enabled && !gate.passed }"
    role="region"
    aria-label="测试报告与质量门禁"
  >
    <header class="tr-head">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
        <path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>
      </svg>
      <h3 class="tr-title">测试报告</h3>

      <!-- 门禁裁决徽标 -->
      <span
        v-if="gate.enabled"
        class="gate-badge"
        :class="gate.passed ? 'gate-badge--pass' : 'gate-badge--block'"
        :aria-label="gate.passed ? '质量门禁通过' : '质量门禁阻断'"
      >
        <svg v-if="gate.passed" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>
        <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg>
        {{ gate.passed ? '门禁通过' : '门禁阻断' }}
      </span>
    </header>

    <p v-if="error" class="tr-error" role="alert">{{ error }}</p>

    <template v-else>
      <!-- 门禁阻断原因(醒目) -->
      <div v-if="gate.enabled && !gate.passed" class="gate-reasons" role="alert">
        <strong class="gate-reasons-title">质量门禁未通过,已阻断后续部署:</strong>
        <ul class="gate-reasons-list">
          <li v-for="(reason, i) in gate.reasons" :key="i">{{ reason }}</li>
        </ul>
      </div>

      <!-- 总览统计条 -->
      <div class="tr-summary">
        <div class="stat stat--pass">
          <span class="stat-num">{{ totals.passed }}</span>
          <span class="stat-label">通过</span>
        </div>
        <div class="stat stat--fail" :class="{ 'stat--zero': totals.failed === 0 }">
          <span class="stat-num">{{ totals.failed }}</span>
          <span class="stat-label">失败</span>
        </div>
        <div class="stat stat--skip" :class="{ 'stat--zero': totals.skipped === 0 }">
          <span class="stat-num">{{ totals.skipped }}</span>
          <span class="stat-label">跳过</span>
        </div>
        <div class="stat stat--total">
          <span class="stat-num">{{ totals.total }}</span>
          <span class="stat-label">总用例</span>
        </div>
      </div>

      <!-- 覆盖率条(总览取各阶段最低) -->
      <div v-if="totals.coverage >= 0" class="coverage">
        <div class="coverage-head">
          <span class="coverage-label">行覆盖率</span>
          <span class="coverage-val mono">{{ coverageText(totals.coverage) }}</span>
        </div>
        <div class="coverage-track" role="meter" :aria-valuenow="Math.round(totals.coverage)" aria-valuemin="0" aria-valuemax="100">
          <div
            class="coverage-fill"
            :class="{ 'coverage-fill--low': totals.coverage < 60, 'coverage-fill--mid': totals.coverage >= 60 && totals.coverage < 80 }"
            :style="{ width: `${Math.min(100, Math.max(0, totals.coverage))}%` }"
          />
        </div>
      </div>

      <!-- 各阶段明细(多阶段时) -->
      <ul v-if="reports.length > 1" class="stage-list" role="list">
        <li v-for="r in reports" :key="r.id" class="stage-row">
          <span class="stage-name">{{ r.stageName || '阶段' }}</span>
          <span class="stage-counts mono">
            <span class="sc-pass">{{ r.passed }}✓</span>
            <span v-if="r.failed > 0" class="sc-fail">{{ r.failed }}✗</span>
            <span v-if="r.skipped > 0" class="sc-skip">{{ r.skipped }}⊘</span>
          </span>
          <span class="stage-rate mono">{{ passRate(r) }}%</span>
          <span v-if="r.coverage >= 0" class="stage-cov mono">cov {{ coverageText(r.coverage) }}</span>
          <span
            v-if="r.gateEnabled"
            class="stage-gate"
            :class="r.gatePassed ? 'stage-gate--pass' : 'stage-gate--block'"
            :title="r.gateReason"
          >{{ r.gatePassed ? '门禁✓' : '门禁✗' }}</span>
        </li>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.test-report {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  background: var(--color-card);
  overflow: hidden;
}

.test-report--blocked {
  border-color: var(--color-red-line, var(--color-amber-line));
}

.tr-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 11px 16px;
  background: var(--color-inset);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-dim);
}

.tr-title {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--color-text);
  letter-spacing: -0.01em;
  flex: 1;
}

.gate-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 9px;
  border-radius: var(--rounded-full);
  font-size: 0.72rem;
  font-weight: 600;
}

.gate-badge--pass { color: var(--color-green); background: var(--color-green-soft); }
.gate-badge--block { color: var(--color-red, #c0392b); background: var(--color-red-soft, var(--color-amber-soft)); }

.tr-error {
  padding: 18px 16px;
  font-size: 0.82rem;
  color: var(--color-faint);
  text-align: center;
}

/* ─── gate reasons (blocked) ─────────────────────────────────────────────── */
.gate-reasons {
  margin: 14px 16px 0;
  padding: 12px 14px;
  border-radius: var(--rounded-md);
  background: var(--color-red-soft, var(--color-amber-soft));
  border: 1px solid var(--color-red-line, var(--color-amber-line));
}

.gate-reasons-title {
  display: block;
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--color-red, #c0392b);
  margin-bottom: 6px;
}

.gate-reasons-list {
  margin: 0;
  padding-left: 18px;
  font-size: 0.8rem;
  color: var(--color-dim);
  display: flex;
  flex-direction: column;
  gap: 3px;
}

/* ─── summary stat strip ─────────────────────────────────────────────────── */
.tr-summary {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 1px;
  padding: 16px;
  background: var(--color-border);
  margin: 16px;
  border-radius: var(--rounded-md);
  overflow: hidden;
}

.stat {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 3px;
  padding: 12px 8px;
  background: var(--color-card);
}

.stat-num {
  font-size: 1.5rem;
  font-weight: 700;
  font-family: var(--font-mono);
  line-height: 1;
  letter-spacing: -0.02em;
}

.stat-label {
  font-size: 0.7rem;
  color: var(--color-faint);
  font-weight: 500;
}

.stat--pass .stat-num { color: var(--color-green); }
.stat--fail .stat-num { color: var(--color-red, #c0392b); }
.stat--skip .stat-num { color: var(--color-amber); }
.stat--total .stat-num { color: var(--color-text); }
.stat--zero .stat-num { color: var(--color-faint); }

/* ─── coverage bar ───────────────────────────────────────────────────────── */
.coverage {
  padding: 0 16px 16px;
}

.coverage-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 6px;
}

.coverage-label {
  font-size: 0.78rem;
  color: var(--color-dim);
  font-weight: 500;
}

.coverage-val {
  font-size: 0.86rem;
  font-weight: 700;
  color: var(--color-text);
}

.coverage-track {
  height: 8px;
  border-radius: var(--rounded-full);
  background: var(--color-inset);
  overflow: hidden;
}

.coverage-fill {
  height: 100%;
  border-radius: var(--rounded-full);
  background: var(--color-green);
  transition: width var(--duration-normal) var(--ease-out, ease);
}

.coverage-fill--mid { background: var(--color-amber); }
.coverage-fill--low { background: var(--color-red, #c0392b); }

/* ─── per-stage detail ───────────────────────────────────────────────────── */
.stage-list {
  list-style: none;
  margin: 0;
  padding: 0 16px 14px;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.stage-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 9px 0;
  border-top: 1px solid var(--color-border);
  font-size: 0.8rem;
}

.stage-name {
  flex: 1;
  font-weight: 600;
  color: var(--color-text);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.stage-counts { display: inline-flex; gap: 8px; }
.sc-pass { color: var(--color-green); }
.sc-fail { color: var(--color-red, #c0392b); }
.sc-skip { color: var(--color-amber); }

.stage-rate, .stage-cov {
  color: var(--color-dim);
  font-size: 0.76rem;
}

.stage-gate {
  font-size: 0.7rem;
  font-weight: 600;
  padding: 2px 7px;
  border-radius: var(--rounded-sm);
}

.stage-gate--pass { color: var(--color-green); background: var(--color-green-soft); }
.stage-gate--block { color: var(--color-red, #c0392b); background: var(--color-red-soft, var(--color-amber-soft)); }

.mono { font-family: var(--font-mono); }

@media (max-width: 640px) {
  .tr-summary { grid-template-columns: repeat(2, 1fr); }
  .stage-row { flex-wrap: wrap; gap: 6px 10px; }
}
</style>
