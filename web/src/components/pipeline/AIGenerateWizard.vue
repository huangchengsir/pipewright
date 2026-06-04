<script setup lang="ts">
/**
 * AIGenerateWizard — AI pipeline generation wizard (modal overlay).
 *
 * States:
 *   idle       → NL supplement input + 生成 button
 *   generating → skeleton loading state
 *   result     → analysis card + proposal preview with per-item checkboxes
 *   unavailable→ friendly 未配置 AI 提供商 guidance card
 *
 * Emits:
 *   close        → parent closes wizard
 *   applied      → parent should reload pipeline / settings / triggers
 */

import { ref, computed, watch } from 'vue'
import {
  aiGenerate,
  aiApply,
  type AIGenerateResponse,
  type AIProposal,
  type AIProposalStage,
  type AIProposalBranchMapping,
} from '../../api/aiGenerate'
import { HttpError } from '../../api/http'
import AppButton from '../ui/AppButton.vue'
import AppBanner from '../ui/AppBanner.vue'
import SkeletonBlock from '../ui/SkeletonBlock.vue'

// ─── Props / Emits ────────────────────────────────────────────────────────────

const props = defineProps<{
  projectId: string
}>()

const emit = defineEmits<{
  close: []
  applied: []
}>()

// ─── State machine ────────────────────────────────────────────────────────────

type WizardPhase = 'idle' | 'generating' | 'result' | 'applying'

const phase         = ref<WizardPhase>('idle')
const nlSupplement  = ref('')
const generateError = ref('')
const applyError    = ref('')
const applySuccess  = ref(false)

const response = ref<AIGenerateResponse | null>(null)

// ─── Selection state ──────────────────────────────────────────────────────────

/** Set of selected stage IDs */
const selectedStageIds    = ref<Set<string>>(new Set())
const buildSelected       = ref(true)
const selectedMappingIds  = ref<Set<string>>(new Set())

function initSelections(proposal: AIProposal): void {
  selectedStageIds.value   = new Set(proposal.stages.map((s) => s.id))
  buildSelected.value      = true
  selectedMappingIds.value = new Set(proposal.branchMappings.map((m) => m.id))
}

function toggleStage(id: string): void {
  const s = new Set(selectedStageIds.value)
  if (s.has(id)) { s.delete(id) } else { s.add(id) }
  selectedStageIds.value = s
}

function toggleMapping(id: string): void {
  const s = new Set(selectedMappingIds.value)
  if (s.has(id)) { s.delete(id) } else { s.add(id) }
  selectedMappingIds.value = s
}

/** Are all selectable items checked? */
const allSelected = computed<boolean>(() => {
  if (!response.value?.proposal) return false
  const p = response.value.proposal
  return (
    p.stages.every((s) => selectedStageIds.value.has(s.id)) &&
    buildSelected.value &&
    p.branchMappings.every((m) => selectedMappingIds.value.has(m.id))
  )
})

const noneSelected = computed<boolean>(() => {
  if (!response.value?.proposal) return true
  return (
    selectedStageIds.value.size === 0 &&
    !buildSelected.value &&
    selectedMappingIds.value.size === 0
  )
})

function selectAll(): void {
  if (!response.value?.proposal) return
  initSelections(response.value.proposal)
}

function deselectAll(): void {
  selectedStageIds.value   = new Set()
  buildSelected.value      = false
  selectedMappingIds.value = new Set()
}

// ─── Generate ─────────────────────────────────────────────────────────────────

async function generate(): Promise<void> {
  phase.value         = 'generating'
  generateError.value = ''
  applyError.value    = ''
  applySuccess.value  = false
  response.value      = null

  try {
    const res = await aiGenerate(props.projectId, {
      nlSupplement: nlSupplement.value.trim() || undefined,
    })
    response.value = res
    if (res.available && res.proposal) {
      initSelections(res.proposal)
    }
    phase.value = 'result'
  } catch (err) {
    if (err instanceof HttpError) {
      generateError.value = err.apiError?.message ?? `请求失败(${err.status})`
    } else {
      generateError.value = '网络错误,请稍后重试。'
    }
    phase.value = 'idle'
  }
}

// ─── Apply ────────────────────────────────────────────────────────────────────

async function apply(): Promise<void> {
  if (!response.value?.proposal) return
  phase.value      = 'applying'
  applyError.value = ''

  try {
    await aiApply(props.projectId, {
      proposal: response.value.proposal,
      selections: {
        stageIds: [...selectedStageIds.value],
        build: buildSelected.value,
        branchMappingIds: [...selectedMappingIds.value],
      },
    })
    applySuccess.value = true
    emit('applied')
    // Brief pause then close so user sees the success state
    setTimeout(() => { emit('close') }, 1200)
  } catch (err) {
    if (err instanceof HttpError) {
      applyError.value = err.apiError?.message ?? `应用失败(${err.status})`
    } else {
      applyError.value = '网络错误,请稍后重试。'
    }
    phase.value = 'result'
  }
}

// ─── Computed helpers ─────────────────────────────────────────────────────────

const showUnavailable = computed<boolean>(() =>
  phase.value === 'result' && response.value !== null && !response.value.available,
)

const showDegradedClone = computed<boolean>(() =>
  phase.value === 'result' &&
  response.value?.available === true &&
  response.value?.analysis?.cloned === false,
)

const showLlmWarn = computed<boolean>(() =>
  phase.value === 'result' &&
  response.value?.available === true &&
  !!response.value?.reason,
)

const isApplying = computed<boolean>(() => phase.value === 'applying')

// ─── Kind labels ──────────────────────────────────────────────────────────────

function kindLabel(kind: string): string {
  const map: Record<string, string> = {
    source:  '源阶段',
    build:   '构建阶段',
    deploy:  '部署阶段',
    notify:  '通知阶段',
    custom:  '自定义',
  }
  return map[kind] ?? kind
}

// Reset when re-opened
watch(() => props.projectId, () => {
  phase.value = 'idle'
  nlSupplement.value = ''
  response.value = null
  generateError.value = ''
  applyError.value = ''
  applySuccess.value = false
})
</script>

<template>
  <!-- ─── Backdrop ──────────────────────────────────────────────────────────── -->
  <div
    class="wiz-backdrop"
    aria-modal="true"
    role="dialog"
    aria-label="AI 生成流水线向导"
    @click.self="emit('close')"
  >
    <!-- ─── Panel ──────────────────────────────────────────────────────────── -->
    <div class="wiz-panel">

      <!-- ── Header ─────────────────────────────────────────────────────────── -->
      <div class="wiz-header">
        <div class="wiz-header-icon" aria-hidden="true">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 12h3.5l2.2-6 3.6 12 2.4-7 1.3 1h4.5"/>
          </svg>
        </div>
        <div class="wiz-header-text">
          <h2 class="wiz-title">AI 生成流水线</h2>
          <p class="wiz-subtitle">分析仓库技术栈,智能推荐配置</p>
        </div>
        <button
          class="wiz-close"
          aria-label="关闭向导"
          @click="emit('close')"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <path d="M18 6 6 18M6 6l12 12"/>
          </svg>
        </button>
      </div>

      <!-- ── Body ───────────────────────────────────────────────────────────── -->
      <div class="wiz-body">

        <!-- ─── Apply success overlay ────────────────────────────────────── -->
        <div v-if="applySuccess" class="wiz-success" aria-live="polite">
          <div class="success-icon" aria-hidden="true">
            <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
              <path d="M20 6 9 17l-5-5"/>
            </svg>
          </div>
          <p class="success-text">已成功应用,画布即将刷新…</p>
        </div>

        <!-- ─── Generating skeleton ──────────────────────────────────────── -->
        <div v-else-if="phase === 'generating'" class="wiz-skeleton" aria-busy="true" aria-label="AI 分析中">
          <div class="skel-section">
            <SkeletonBlock :width="120" :height="13" />
            <div class="skel-row">
              <SkeletonBlock width="48%" :height="52" :rounded="true" />
              <SkeletonBlock width="48%" :height="52" :rounded="true" />
            </div>
            <SkeletonBlock width="100%" :height="38" :rounded="true" />
          </div>
          <div class="skel-section">
            <SkeletonBlock :width="90" :height="13" />
            <SkeletonBlock width="100%" :height="70" :rounded="true" />
            <SkeletonBlock width="100%" :height="70" :rounded="true" />
          </div>
          <div class="skel-generating-label" aria-hidden="true">
            <span class="skel-dot" /><span class="skel-dot" /><span class="skel-dot" />
            <span>AI 正在分析仓库…</span>
          </div>
        </div>

        <!-- ─── Result phase ──────────────────────────────────────────────── -->
        <div v-else-if="phase === 'result' || phase === 'applying'" class="wiz-result">

          <!-- Unavailable guidance card -->
          <div v-if="showUnavailable" class="unavail-card">
            <div class="unavail-icon" aria-hidden="true">
              <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
                <circle cx="12" cy="12" r="9"/>
                <path d="M12 8v5M12 16h.01"/>
              </svg>
            </div>
            <h3 class="unavail-title">AI 提供商未配置</h3>
            <p class="unavail-desc">{{ response?.reason || 'AI 提供商未配置,可在设置中启用后再尝试智能生成。' }}</p>
            <div class="unavail-actions">
              <router-link
                to="/settings/ai"
                class="unavail-link"
                @click="emit('close')"
              >
                前往设置 · AI 提供商
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                  <path d="M7 17 17 7M7 7h10v10"/>
                </svg>
              </router-link>
              <AppButton variant="ghost" @click="emit('close')">
                手动配置流水线
              </AppButton>
            </div>
          </div>

          <!-- Available result -->
          <template v-else-if="response">

            <!-- Clone degraded banner -->
            <AppBanner v-if="showDegradedClone" variant="warn" class="wiz-inner-banner">
              未能拉取仓库代码,基于仓库地址和补充描述进行推断,配置仅供参考。
            </AppBanner>

            <!-- LLM failure warning banner -->
            <AppBanner v-if="showLlmWarn" variant="warn" class="wiz-inner-banner">
              {{ response.reason }}
            </AppBanner>

            <!-- Analysis card -->
            <section class="analysis-card" aria-label="仓库技术栈分析">
              <div class="analysis-card-head">
                <span class="analysis-card-icon" aria-hidden="true">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <circle cx="11" cy="11" r="7"/><path d="m21 21-4.35-4.35"/>
                  </svg>
                </span>
                <span class="analysis-card-label">仓库分析</span>
                <span
                  class="analysis-clone-badge"
                  :class="response.analysis.cloned ? 'badge--ok' : 'badge--warn'"
                >
                  {{ response.analysis.cloned ? '已克隆' : '降级推断' }}
                </span>
              </div>

              <div class="analysis-grid">
                <div class="analysis-item">
                  <span class="analysis-item-key">语言</span>
                  <span class="analysis-item-val">{{ response.analysis.language || '未识别' }}</span>
                </div>
                <div class="analysis-item">
                  <span class="analysis-item-key">版本</span>
                  <span class="analysis-item-val">{{ response.analysis.languageVersion || '—' }}</span>
                </div>
                <div class="analysis-item">
                  <span class="analysis-item-key">构建工具</span>
                  <span class="analysis-item-val">{{ response.analysis.buildTool || '—' }}</span>
                </div>
                <div class="analysis-item">
                  <span class="analysis-item-key">Dockerfile</span>
                  <span class="analysis-item-val">{{ response.analysis.hasDockerfile ? '已检测' : '无' }}</span>
                </div>
              </div>

              <div v-if="response.analysis.signals.length" class="analysis-signals">
                <span class="analysis-signals-label">检测到</span>
                <span
                  v-for="sig in response.analysis.signals"
                  :key="sig"
                  class="signal-tag"
                  :title="sig"
                >{{ sig }}</span>
              </div>
            </section>

            <!-- 提案区:守 null proposal(LLM 失败降级时后端返 available=true + proposal=null,
                 此处不渲染提案以免解引用崩溃;仅显上方警告 banner + 分析卡)。 -->
            <template v-if="response.proposal">
            <!-- Rationale -->
            <div v-if="response.proposal.rationale" class="rationale-text">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
              </svg>
              {{ response.proposal.rationale }}
            </div>

            <!-- Proposal preview -->
            <section class="proposal-section" aria-label="流水线提案">
              <div class="proposal-header">
                <span class="proposal-header-title">提案预览</span>
                <div class="proposal-header-actions">
                  <button
                    class="sel-btn"
                    :class="{ 'sel-btn--active': allSelected }"
                    @click="allSelected ? deselectAll() : selectAll()"
                  >
                    {{ allSelected ? '全部取消' : '全部选择' }}
                  </button>
                </div>
              </div>

              <!-- Stages -->
              <div class="proposal-group">
                <div class="proposal-group-label">流水线阶段</div>
                <div
                  v-for="stage in response.proposal.stages"
                  :key="stage.id"
                  class="proposal-item"
                  :class="{ 'proposal-item--selected': selectedStageIds.has(stage.id) }"
                >
                  <label class="proposal-item-check">
                    <input
                      type="checkbox"
                      class="prop-checkbox"
                      :checked="selectedStageIds.has(stage.id)"
                      :aria-label="`选择阶段:${stage.name}`"
                      @change="toggleStage(stage.id)"
                    />
                    <span class="prop-checkbox-visual" aria-hidden="true" />
                  </label>
                  <div class="proposal-item-content">
                    <div class="proposal-item-head">
                      <span class="proposal-item-name">{{ stage.name }}</span>
                      <span class="proposal-kind-badge">{{ kindLabel(stage.kind) }}</span>
                    </div>
                    <div v-if="stage.jobs.length" class="proposal-jobs">
                      <div
                        v-for="job in stage.jobs"
                        :key="job.id"
                        class="proposal-job"
                      >
                        <div class="proposal-job-head">
                          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                            <rect x="3" y="4" width="18" height="6" rx="1.5"/>
                            <rect x="3" y="14" width="18" height="6" rx="1.5"/>
                          </svg>
                          <span class="job-name">{{ job.name }}</span>
                          <span class="job-type">{{ job.type }}</span>
                          <span v-if="job.summary" class="job-summary">— {{ job.summary }}</span>
                        </div>
                        <!-- 串/并:有 needs → 串行(显示依赖);无 needs → 与同阶段其它节点并行 -->
                        <div v-if="job.needs && job.needs.length" class="job-needs">
                          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><path d="M5 12h14M13 6l6 6-6 6"/></svg>
                          <span>串行 · 依赖 {{ job.needs.join('、') }}</span>
                        </div>
                        <div v-else class="job-needs job-needs--parallel">
                          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><path d="M8 6v12M16 6v12"/></svg>
                          <span>并行</span>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              <!-- Build config -->
              <div class="proposal-group">
                <div class="proposal-group-label">构建配置</div>
                <div
                  class="proposal-item"
                  :class="{ 'proposal-item--selected': buildSelected }"
                >
                  <label class="proposal-item-check">
                    <input
                      type="checkbox"
                      class="prop-checkbox"
                      :checked="buildSelected"
                      aria-label="选择构建配置"
                      @change="buildSelected = !buildSelected"
                    />
                    <span class="prop-checkbox-visual" aria-hidden="true" />
                  </label>
                  <div class="proposal-item-content">
                    <div class="proposal-item-head">
                      <span class="proposal-item-name">{{ response.proposal.build.model === 'toolchain' ? '工具链构建' : 'Dockerfile 构建' }}</span>
                      <span class="proposal-kind-badge">build</span>
                    </div>
                    <div class="proposal-build-meta">
                      <template v-if="response.proposal.build.model === 'toolchain'">
                        <span class="build-meta-chip">{{ response.proposal.build.toolchain.language }}:{{ response.proposal.build.toolchain.version }}</span>
                      </template>
                      <template v-else-if="response.proposal.build.dockerfilePath">
                        <span class="build-meta-chip">{{ response.proposal.build.dockerfilePath }}</span>
                      </template>
                      <span class="build-meta-chip">产物: {{ response.proposal.build.artifactType }}</span>
                    </div>
                  </div>
                </div>
              </div>

              <!-- Branch mappings -->
              <div v-if="response.proposal.branchMappings.length" class="proposal-group">
                <div class="proposal-group-label">分支映射</div>
                <div
                  v-for="mapping in response.proposal.branchMappings"
                  :key="mapping.id"
                  class="proposal-item"
                  :class="{ 'proposal-item--selected': selectedMappingIds.has(mapping.id) }"
                >
                  <label class="proposal-item-check">
                    <input
                      type="checkbox"
                      class="prop-checkbox"
                      :checked="selectedMappingIds.has(mapping.id)"
                      :aria-label="`选择分支映射:${mapping.branchPattern} → ${mapping.environment}`"
                      @change="toggleMapping(mapping.id)"
                    />
                    <span class="prop-checkbox-visual" aria-hidden="true" />
                  </label>
                  <div class="proposal-item-content">
                    <div class="proposal-item-head">
                      <span class="proposal-item-name">
                        <code class="branch-code">{{ mapping.branchPattern }}</code>
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                          <path d="M5 12h14M13 6l6 6-6 6"/>
                        </svg>
                        {{ mapping.environment }}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </section>
            </template><!-- /v-if response.proposal -->

            <!-- Apply error banner -->
            <AppBanner v-if="applyError" variant="error" class="wiz-inner-banner">
              {{ applyError }}
            </AppBanner>

          </template>
        </div>

        <!-- ─── Idle phase: NL input ──────────────────────────────────────── -->
        <div v-else class="wiz-idle">
          <div class="idle-hint">
            <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" class="idle-hint-icon" aria-hidden="true">
              <path d="M3 12h3.5l2.2-6 3.6 12 2.4-7 1.3 1h4.5"/>
            </svg>
            <p class="idle-hint-text">AI 将分析项目仓库的技术栈与结构,为你生成一份合理的流水线配置提案。你可以逐项勾选接受。</p>
          </div>

          <div class="nl-field">
            <label for="wiz-nl-input" class="nl-label">补充说明（可选）</label>
            <textarea
              id="wiz-nl-input"
              v-model="nlSupplement"
              class="nl-textarea"
              rows="3"
              placeholder="例:push main 分支部署到生产,develop 分支部署到测试环境"
              aria-label="自然语言补充说明"
              @keydown.ctrl.enter="generate"
              @keydown.meta.enter="generate"
            />
            <p class="nl-hint">Ctrl+Enter / ⌘+Enter 快速生成</p>
          </div>

          <AppBanner v-if="generateError" variant="error" class="wiz-inner-banner">
            {{ generateError }}
          </AppBanner>
        </div>

      </div><!-- /wiz-body -->

      <!-- ── Footer ─────────────────────────────────────────────────────────── -->
      <div class="wiz-footer">

        <!-- Idle footer -->
        <template v-if="phase === 'idle'">
          <AppButton variant="ghost" @click="emit('close')">取消</AppButton>
          <AppButton
            variant="ai"
            :loading="false"
            @click="generate"
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M3 12h3.5l2.2-6 3.6 12 2.4-7 1.3 1h4.5"/>
            </svg>
            生成配置
          </AppButton>
        </template>

        <!-- Generating footer -->
        <template v-else-if="phase === 'generating'">
          <AppButton variant="ghost" disabled>取消</AppButton>
          <AppButton variant="ai" :loading="true">AI 分析中…</AppButton>
        </template>

        <!-- Result footer -->
        <template v-else-if="phase === 'result' || phase === 'applying'">
          <!-- Unavailable: only close button -->
          <template v-if="showUnavailable">
            <AppButton variant="ghost" @click="emit('close')">关闭</AppButton>
          </template>

          <!-- Available result footer -->
          <template v-else>
            <AppButton variant="ghost" @click="phase = 'idle'">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                <path d="M1 4v6h6M23 20v-6h-6"/><path d="M20.49 9A9 9 0 0 0 5.64 5.64L1 10M23 14l-4.64 4.36A9 9 0 0 1 3.51 15"/>
              </svg>
              重新生成
            </AppButton>

            <!-- 应用按钮仅在有提案时显示;LLM 失败降级(proposal=null)时只能重新生成/关闭。 -->
            <div v-if="response && response.proposal" class="footer-right">
              <AppButton
                variant="default"
                :disabled="noneSelected || isApplying"
                :loading="isApplying"
                @click="apply"
              >
                应用所选
              </AppButton>
              <AppButton
                variant="primary"
                :disabled="isApplying"
                :loading="isApplying"
                @click="selectAll(); apply()"
              >
                全部接受
              </AppButton>
            </div>
            <AppButton v-else variant="ghost" @click="emit('close')">关闭</AppButton>
          </template>
        </template>

      </div><!-- /wiz-footer -->

    </div><!-- /wiz-panel -->
  </div><!-- /wiz-backdrop -->
</template>

<style scoped>
/* ─── Backdrop ───────────────────────────────────────────────────────────── */
.wiz-backdrop {
  position: fixed;
  inset: 0;
  z-index: 900;
  background: oklch(0% 0 0 / 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  animation: backdrop-in var(--duration-normal) var(--ease-out-expo);
}

@keyframes backdrop-in {
  from { opacity: 0; }
  to   { opacity: 1; }
}

@media (prefers-reduced-motion: reduce) {
  .wiz-backdrop { animation: none; }
}

/* ─── Panel ──────────────────────────────────────────────────────────────── */
.wiz-panel {
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow-modal);
  width: 100%;
  max-width: 620px;
  max-height: calc(100vh - 48px);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  animation: panel-in var(--duration-normal) var(--ease-out-expo);
}

@keyframes panel-in {
  from { opacity: 0; transform: translateY(14px) scale(0.98); }
  to   { opacity: 1; transform: translateY(0) scale(1); }
}

@media (prefers-reduced-motion: reduce) {
  .wiz-panel { animation: none; }
}

/* ─── Header ─────────────────────────────────────────────────────────────── */
.wiz-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 18px 20px 14px;
  border-bottom: 1px solid var(--color-border);
  flex: none;
}

.wiz-header-icon {
  width: 34px;
  height: 34px;
  border-radius: var(--rounded-lg);
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  color: var(--color-cyan);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.wiz-header-text {
  flex: 1;
  min-width: 0;
}

.wiz-title {
  font-size: 0.98rem;
  font-weight: 700;
  color: var(--color-text);
  margin: 0 0 2px;
  letter-spacing: -0.01em;
}

.wiz-subtitle {
  font-size: 0.78rem;
  color: var(--color-faint);
  margin: 0;
}

.wiz-close {
  width: 30px;
  height: 30px;
  border: none;
  background: none;
  cursor: pointer;
  color: var(--color-dim);
  border-radius: var(--rounded-md);
  display: grid;
  place-items: center;
  transition: color var(--duration-fast), background var(--duration-fast);
  flex-shrink: 0;
}

.wiz-close:hover { color: var(--color-text); background: var(--color-inset); }
.wiz-close:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }

/* ─── Body ───────────────────────────────────────────────────────────────── */
.wiz-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* ─── Success overlay ────────────────────────────────────────────────────── */
.wiz-success {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  padding: 48px 20px;
}

.success-icon {
  width: 56px;
  height: 56px;
  border-radius: var(--rounded-full);
  background: var(--color-green-soft);
  border: 1px solid var(--color-green-line);
  color: var(--color-green);
  display: grid;
  place-items: center;
  animation: success-pop var(--duration-normal) var(--ease-out-expo);
}

@keyframes success-pop {
  from { transform: scale(0.7); opacity: 0; }
  to   { transform: scale(1); opacity: 1; }
}

@media (prefers-reduced-motion: reduce) {
  .success-icon { animation: none; }
}

.success-text {
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--color-green);
}

/* ─── Skeleton / generating ──────────────────────────────────────────────── */
.wiz-skeleton {
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.skel-section {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.skel-row {
  display: flex;
  gap: 12px;
}

.skel-generating-label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.78rem;
  color: var(--color-faint);
  padding-top: 4px;
}

.skel-dot {
  width: 5px;
  height: 5px;
  border-radius: var(--rounded-full);
  background: var(--color-cyan);
  animation: dot-pulse 1.2s ease-in-out infinite;
}

.skel-dot:nth-child(2) { animation-delay: 0.2s; }
.skel-dot:nth-child(3) { animation-delay: 0.4s; }

@keyframes dot-pulse {
  0%, 100% { opacity: 0.3; transform: scale(0.8); }
  50%       { opacity: 1;   transform: scale(1.1); }
}

@media (prefers-reduced-motion: reduce) {
  .skel-dot { animation: none; opacity: 0.6; }
}

/* ─── Idle ───────────────────────────────────────────────────────────────── */
.wiz-idle {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.idle-hint {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  padding: 14px 16px;
  background: var(--color-inset);
  border-radius: var(--rounded-lg);
  border: 1px solid var(--color-border);
}

.idle-hint-icon {
  color: var(--color-cyan);
  flex-shrink: 0;
  margin-top: 2px;
}

.idle-hint-text {
  font-size: 0.83rem;
  color: var(--color-dim);
  line-height: 1.6;
  margin: 0;
}

.nl-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.nl-label {
  font-size: var(--text-label);
  font-weight: 500;
  color: var(--color-dim);
}

.nl-textarea {
  width: 100%;
  padding: 10px 12px;
  font-family: var(--font-sans);
  font-size: 0.84rem;
  color: var(--color-text);
  background: var(--color-inset);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  resize: vertical;
  min-height: 72px;
  line-height: 1.55;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
  box-sizing: border-box;
}

.nl-textarea::placeholder { color: var(--color-faint); }

.nl-textarea:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

.nl-hint {
  font-size: 0.73rem;
  color: var(--color-faint);
  margin: 0;
}

/* ─── Inner banners ──────────────────────────────────────────────────────── */
.wiz-inner-banner {
  /* AppBanner already handles styling — just ensure zero extra margin */
  flex-shrink: 0;
}

/* ─── Result ─────────────────────────────────────────────────────────────── */
.wiz-result {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* ─── Unavailable card ───────────────────────────────────────────────────── */
.unavail-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 36px 24px;
  text-align: center;
  background: var(--color-inset);
  border: 1.5px dashed var(--color-border-strong);
  border-radius: var(--rounded-card);
}

.unavail-icon {
  width: 52px;
  height: 52px;
  border-radius: var(--rounded-full);
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  color: var(--color-amber);
  display: grid;
  place-items: center;
}

.unavail-title {
  font-size: 0.98rem;
  font-weight: 700;
  color: var(--color-text);
  margin: 0;
}

.unavail-desc {
  font-size: 0.82rem;
  color: var(--color-dim);
  line-height: 1.6;
  max-width: 38ch;
  margin: 0;
}

.unavail-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  justify-content: center;
  margin-top: 4px;
}

.unavail-link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 0.83rem;
  font-weight: 600;
  color: var(--color-cyan);
  text-decoration: none;
  padding: 7px 14px;
  border: 1px solid var(--color-cyan-line);
  border-radius: var(--rounded);
  background: var(--color-cyan-soft);
  transition: box-shadow var(--duration-fast);
}

.unavail-link:hover { box-shadow: 0 0 0 3px var(--color-cyan-soft); }
.unavail-link:focus-visible { outline: 2px solid var(--color-cyan); outline-offset: 2px; border-radius: var(--rounded); }

/* ─── Analysis card ──────────────────────────────────────────────────────── */
.analysis-card {
  background: var(--color-card-2);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg);
  padding: 14px 16px 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.analysis-card-head {
  display: flex;
  align-items: center;
  gap: 7px;
}

.analysis-card-icon {
  color: var(--color-cyan);
  display: flex;
  align-items: center;
}

.analysis-card-label {
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-dim);
  flex: 1;
}

.analysis-clone-badge {
  font-size: var(--text-micro);
  font-weight: 700;
  padding: 2px 8px;
  border-radius: var(--rounded-full);
  border: 1px solid transparent;
}

.badge--ok {
  background: var(--color-green-soft);
  color: var(--color-green);
  border-color: var(--color-green-line);
}

.badge--warn {
  background: var(--color-amber-soft);
  color: var(--color-amber);
  border-color: var(--color-amber-line);
}

.analysis-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 8px;
}

.analysis-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  background: var(--color-inset);
  border-radius: var(--rounded-md);
  padding: 8px 10px;
}

.analysis-item-key {
  font-size: var(--text-micro);
  color: var(--color-faint);
  font-weight: 500;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.analysis-item-val {
  font-size: 0.84rem;
  font-weight: 600;
  color: var(--color-text);
  font-family: var(--font-mono);
}

.analysis-signals {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.analysis-signals-label {
  font-size: var(--text-micro);
  color: var(--color-faint);
  font-weight: 500;
}

.signal-tag {
  font-size: 0.73rem;
  font-family: var(--font-mono);
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  border-radius: var(--rounded-sm);
  padding: 1px 7px;
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ─── Rationale text ─────────────────────────────────────────────────────── */
.rationale-text {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: 0.81rem;
  color: var(--color-dim);
  line-height: 1.6;
  padding: 10px 14px;
  background: var(--color-inset);
  border-radius: var(--rounded-md);
  border-left: 2px solid var(--color-cyan-line);
}

.rationale-text svg {
  color: var(--color-cyan);
  flex-shrink: 0;
  margin-top: 2px;
}

/* ─── Proposal ───────────────────────────────────────────────────────────── */
.proposal-section {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.proposal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.proposal-header-title {
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-dim);
}

.proposal-header-actions {
  display: flex;
  gap: 6px;
}

.sel-btn {
  font-size: 0.76rem;
  font-weight: 600;
  color: var(--color-faint);
  background: none;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-sm);
  padding: 3px 9px;
  cursor: pointer;
  font-family: var(--font-sans);
  transition: color var(--duration-fast), border-color var(--duration-fast);
}

.sel-btn:hover { color: var(--color-dim); border-color: var(--color-faint); }
.sel-btn--active { color: var(--color-primary); border-color: var(--color-primary); }
.sel-btn:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }

.proposal-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.proposal-group-label {
  font-size: var(--text-micro);
  font-weight: 700;
  color: var(--color-faint);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  padding: 0 2px;
}

/* ─── Proposal item (checkbox row) ──────────────────────────────────────── */
.proposal-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  background: var(--color-card-2);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
  cursor: default;
  transition: border-color var(--duration-fast), background var(--duration-fast);
}

.proposal-item:hover {
  border-color: var(--color-border-strong);
}

.proposal-item--selected {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}

.proposal-item-check {
  display: flex;
  align-items: center;
  cursor: pointer;
  flex-shrink: 0;
  margin-top: 1px;
  position: relative;
}

/* visually hide native checkbox, show custom visual */
.prop-checkbox {
  position: absolute;
  opacity: 0;
  width: 16px;
  height: 16px;
  margin: 0;
  cursor: pointer;
}

.prop-checkbox-visual {
  width: 16px;
  height: 16px;
  border-radius: 4px;
  border: 1.5px solid var(--color-border-strong);
  background: var(--color-inset);
  display: block;
  transition: border-color var(--duration-fast), background var(--duration-fast);
  pointer-events: none;
  position: relative;
}

.proposal-item--selected .prop-checkbox-visual {
  background: var(--color-primary);
  border-color: var(--color-primary);
}

.proposal-item--selected .prop-checkbox-visual::after {
  content: '';
  position: absolute;
  left: 4px;
  top: 1.5px;
  width: 5px;
  height: 8px;
  border: 1.8px solid #fff;
  border-top: none;
  border-left: none;
  transform: rotate(42deg);
}

.prop-checkbox:focus-visible ~ .prop-checkbox-visual {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.proposal-item-content {
  flex: 1;
  min-width: 0;
}

.proposal-item-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.proposal-item-name {
  font-size: 0.84rem;
  font-weight: 600;
  color: var(--color-text);
  display: flex;
  align-items: center;
  gap: 6px;
}

.proposal-kind-badge {
  font-size: var(--text-micro);
  font-weight: 600;
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  border-radius: var(--rounded-full);
  padding: 1px 7px;
}

.proposal-jobs {
  display: flex;
  flex-direction: column;
  gap: 3px;
  margin-top: 6px;
}

.proposal-job {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 0.76rem;
  color: var(--color-faint);
}

.proposal-job-head {
  display: flex;
  align-items: center;
  gap: 5px;
}

.proposal-job svg { color: var(--color-faint); flex-shrink: 0; }

/* 串/并依赖指示:让预览体现节点编排(串行显依赖、并行显并行),不只是平铺列表。 */
.job-needs {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-left: 16px;
  font-size: 0.68rem;
  color: var(--color-cyan);
}
.job-needs--parallel { color: var(--color-faint); }
.job-needs svg { color: inherit; }

.job-name {
  font-weight: 500;
  color: var(--color-dim);
}

.job-type {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--color-faint);
  background: var(--color-inset);
  border-radius: var(--rounded-sm);
  padding: 0 5px;
}

.job-summary {
  color: var(--color-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.proposal-build-meta {
  display: flex;
  gap: 6px;
  margin-top: 5px;
  flex-wrap: wrap;
}

.build-meta-chip {
  font-size: 0.73rem;
  font-family: var(--font-mono);
  color: var(--color-dim);
  background: var(--color-inset);
  border-radius: var(--rounded-sm);
  padding: 1px 7px;
  border: 1px solid var(--color-border);
}

.branch-code {
  font-family: var(--font-mono);
  font-size: 0.78rem;
  color: var(--color-primary);
  background: var(--color-primary-soft);
  padding: 1px 6px;
  border-radius: var(--rounded-sm);
}

/* ─── Footer ─────────────────────────────────────────────────────────────── */
.wiz-footer {
  padding: 14px 20px;
  border-top: 1px solid var(--color-border);
  display: flex;
  align-items: center;
  gap: 10px;
  flex: none;
  background: var(--color-card);
}

.footer-right {
  display: flex;
  gap: 8px;
  margin-left: auto;
}

/* push first button to left, rest to right when no footer-right */
.wiz-footer > :first-child:not(.footer-right) {
  margin-right: auto;
}
</style>
