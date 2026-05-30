<script setup lang="ts">
/**
 * SettingsAI (Story 7.1) — AI provider configuration (FR-24).
 *
 * Delivers:
 *   - Provider selection cards (Claude / OpenAI / Ollama)
 *   - baseUrl auto-filled by provider, editable
 *   - model text input
 *   - apiKey: WRITE-ONLY — never echoed from server; masked placeholder when configured
 *   - Ollama hides apiKey (no key needed for local provider)
 *   - budget (monthly token limit, nullable)
 *   - enabled toggle
 *   - "测试连接" with ok/latency/error result
 *   - Save with 422 error mapping
 *   - Unconfigured friendly guidance
 *
 * Reuses: FormField / AppButton tokens / vault patterns from 1-6.
 * No new UI libraries. Animation: transform/opacity + prefers-reduced-motion.
 */
import { ref, computed, onMounted } from 'vue'
import FormField from '../../components/ui/FormField.vue'
import AppButton from '../../components/ui/AppButton.vue'
import { useToast } from '../../composables/useToast'
import { HttpError } from '../../api/http'
import {
  getAISettings,
  saveAISettings,
  testAIConnection,
  type AIProvider,
  type AISettings,
  type AITestResult,
} from '../../api/aiSettings'

// ─── types ───────────────────────────────────────────────────────────────────

type LoadState = 'loading' | 'ready' | 'error'

interface ProviderMeta {
  id: AIProvider
  label: string
  desc: string
  logoText: string
  logoStyle: string
  tag?: string
  tagStyle?: string
}

// ─── provider metadata ───────────────────────────────────────────────────────

const PROVIDERS: ProviderMeta[] = [
  {
    id: 'claude',
    label: 'Claude',
    desc: 'Anthropic',
    logoText: 'A',
    logoStyle: 'background:#D97757;color:#fff',
    tag: '诊断推荐',
    tagStyle: 'color:var(--color-primary);background:var(--color-primary-soft)',
  },
  {
    id: 'openai',
    label: 'OpenAI',
    desc: 'GPT-4o / o-series',
    logoText: '○',
    logoStyle: 'background:oklch(70% 0.14 160);color:#0b1f17',
  },
  {
    id: 'ollama',
    label: 'Ollama',
    desc: '本地 / 自托管',
    logoText: 'Ll',
    logoStyle: 'background:var(--color-inset);color:var(--color-dim);font-size:0.72rem',
    tag: '零外发',
    tagStyle: 'color:var(--color-green);background:var(--color-green-soft)',
  },
]

const DEFAULT_BASE_URLS: Record<string, string> = {
  claude: 'https://api.anthropic.com',
  openai: 'https://api.openai.com',
  ollama: 'http://localhost:11434',
}

// ─── toast ────────────────────────────────────────────────────────────────────

const toast = useToast()

// ─── load state ───────────────────────────────────────────────────────────────

const loadState = ref<LoadState>('loading')
const loadError = ref('')
const savedSettings = ref<AISettings | null>(null)

// ─── form state ───────────────────────────────────────────────────────────────

const selectedProvider = ref<AIProvider>('')
const baseUrl = ref('')
const model = ref('')
const apiKey = ref('')          // write-only: blank = keep existing; non-blank = rotate
const monthlyTokenLimit = ref<string>('')  // string for input binding; coerced on save
const enabled = ref(false)

// field errors (422 mapping)
const errors = ref({
  provider: '',
  baseUrl: '',
  apiKey: '',
})

const saving = ref(false)

// ─── test connection state ────────────────────────────────────────────────────

type TestState = 'idle' | 'running' | 'ok' | 'fail'
const testState = ref<TestState>('idle')
const testResult = ref<AITestResult | null>(null)

// ─── derived ──────────────────────────────────────────────────────────────────

const isOllama = computed(() => selectedProvider.value === 'ollama')

/** True when there is an existing configured key in the saved settings */
const hasExistingKey = computed(
  () => !!savedSettings.value?.apiKeyMasked,
)

/** True when apiKey input has been touched (rotation intent) */
const isRotatingKey = computed(() => apiKey.value.length > 0)

const isDirty = computed(() => {
  if (!savedSettings.value) return selectedProvider.value !== ''
  const s = savedSettings.value
  return (
    selectedProvider.value !== s.provider ||
    baseUrl.value !== s.baseUrl ||
    model.value !== s.model ||
    apiKey.value !== '' ||
    String(s.budget.monthlyTokenLimit ?? '') !== monthlyTokenLimit.value ||
    enabled.value !== s.enabled
  )
})

// ─── load ─────────────────────────────────────────────────────────────────────

async function loadSettings(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    const data = await getAISettings()
    applySettings(data)
    loadState.value = 'ready'
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        loadError.value = '无法连接到服务器,请检查后端是否运行后重试'
      } else if (err.apiError?.code === 'vault_unconfigured') {
        loadError.value = '保险库未配置 master key,请设置 PIPEWRIGHT_MASTER_KEY 环境变量'
      } else {
        loadError.value = err.apiError?.message ?? `加载失败(${err.status})`
      }
    } else {
      loadError.value = '加载 AI 配置失败,请稍后重试'
    }
    loadState.value = 'error'
  }
}

function applySettings(data: AISettings): void {
  savedSettings.value = data
  selectedProvider.value = data.provider
  baseUrl.value = data.baseUrl
  model.value = data.model
  apiKey.value = ''  // never pre-fill
  monthlyTokenLimit.value = data.budget.monthlyTokenLimit != null
    ? String(data.budget.monthlyTokenLimit)
    : ''
  enabled.value = data.enabled
}

onMounted(loadSettings)

// ─── provider card selection ──────────────────────────────────────────────────

function selectProvider(id: AIProvider): void {
  if (selectedProvider.value === id) return
  const oldDefault = DEFAULT_BASE_URLS[selectedProvider.value] ?? ''
  const wasDefault = baseUrl.value === '' || baseUrl.value === oldDefault
  selectedProvider.value = id
  if (wasDefault) {
    baseUrl.value = DEFAULT_BASE_URLS[id] ?? ''
  }
  errors.value.provider = ''
  errors.value.apiKey = ''
  testState.value = 'idle'
  testResult.value = null
}

// ─── test connection ──────────────────────────────────────────────────────────

async function handleTest(): Promise<void> {
  testState.value = 'running'
  testResult.value = null
  try {
    const draft: { provider?: AIProvider; baseUrl?: string; model?: string; apiKey?: string } = {}
    if (selectedProvider.value) draft.provider = selectedProvider.value
    if (baseUrl.value)           draft.baseUrl  = baseUrl.value
    if (model.value)             draft.model    = model.value
    if (apiKey.value)            draft.apiKey   = apiKey.value
    const result = await testAIConnection(draft)
    testResult.value = result
    testState.value = result.ok ? 'ok' : 'fail'
  } catch (err) {
    testState.value = 'fail'
    testResult.value = {
      ok: false,
      latencyMs: 0,
      detail: '',
      error: err instanceof HttpError
        ? (err.apiError?.message ?? `请求失败(${err.status})`)
        : err instanceof Error
          ? err.message
          : '未知错误',
    }
  }
}

// ─── save ─────────────────────────────────────────────────────────────────────

function clearErrors(): void {
  errors.value = { provider: '', baseUrl: '', apiKey: '' }
}

async function handleSave(): Promise<void> {
  clearErrors()
  saving.value = true
  try {
    const limit = monthlyTokenLimit.value.trim()
    const parsed = limit === '' ? null : Number(limit)
    if (limit !== '' && (Number.isNaN(parsed) || (parsed !== null && parsed < 0))) {
      saving.value = false
      toast.error('保存失败', { detail: '月 token 上限须为正整数或留空' })
      return
    }

    const payload = {
      provider: selectedProvider.value,
      baseUrl: baseUrl.value.trim(),
      model: model.value.trim(),
      ...(apiKey.value ? { apiKey: apiKey.value } : {}),
      budget: { monthlyTokenLimit: parsed },
      enabled: enabled.value,
    }

    const updated = await saveAISettings(payload)
    applySettings(updated)
    toast.success('AI 配置已保存')
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 422 && err.apiError) {
        const code = err.apiError.code
        if (code === 'invalid_provider') {
          errors.value.provider = '请选择有效的提供商'
        } else if (code === 'base_url_required') {
          errors.value.baseUrl = '请填写接入地址'
        } else if (code === 'api_key_required') {
          errors.value.apiKey = 'API Key 不可为空(非 Ollama 必填)'
        } else {
          toast.error('保存失败', { detail: err.apiError.message })
        }
        return
      }
      if (err.status === 0) {
        toast.error('保存失败', { detail: '无法连接到服务器' })
      } else {
        toast.error('保存失败', { detail: err.apiError?.message ?? `HTTP ${err.status}` })
      }
    } else {
      toast.error('保存失败', { detail: err instanceof Error ? err.message : '未知错误' })
    }
  } finally {
    saving.value = false
  }
}

// ─── discard ─────────────────────────────────────────────────────────────────

function handleDiscard(): void {
  if (savedSettings.value) {
    applySettings(savedSettings.value)
  }
  clearErrors()
  testState.value = 'idle'
  testResult.value = null
}

// ─── helpers ─────────────────────────────────────────────────────────────────

function relativeTime(iso: string | null): string {
  if (!iso) return ''
  const diff = Date.now() - new Date(iso).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return '刚刚'
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} 分钟前`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} 小时前`
  return `${Math.floor(h / 24)} 天前`
}
</script>

<template>
  <div class="ai-root">
    <!-- ─── section header ──────────────────────────────────────────────────── -->
    <div class="section-head">
      <div class="section-head-text">
        <h2 class="section-title">AI 提供商</h2>
        <p class="section-desc">
          Pipewright 不自训模型 —— 接入你自己的 LLM 用于失败诊断与配置生成。密钥仅存于本实例的加密保险库,绝不外泄。
        </p>
      </div>
      <!-- Connection status badge -->
      <div v-if="loadState === 'ready' && savedSettings?.configured" class="status-chip status-chip--ok">
        <span class="status-dot status-dot--ok" aria-hidden="true" />
        已配置
      </div>
      <div v-else-if="loadState === 'ready' && !savedSettings?.configured" class="status-chip status-chip--idle">
        <span class="status-dot" aria-hidden="true" />
        未配置
      </div>
    </div>

    <!-- ─── load error ──────────────────────────────────────────────────────── -->
    <div v-if="loadState === 'error'" class="banner banner--error" role="alert">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
      </svg>
      <span>{{ loadError }}</span>
      <button class="banner-retry" @click="loadSettings">↻ 重试</button>
    </div>

    <!-- ─── loading skeleton ────────────────────────────────────────────────── -->
    <template v-if="loadState === 'loading'">
      <div class="panel panel--anim">
        <div class="panel-head">
          <span class="skel skel--text" style="width:90px" aria-hidden="true" />
        </div>
        <div class="skel-body">
          <div class="prov-grid">
            <div v-for="i in 3" :key="i" class="skel skel--card" aria-hidden="true" />
          </div>
          <div class="skel skel--field" style="height:38px;margin-top:20px" aria-hidden="true" />
          <div class="skel skel--field" style="height:38px" aria-hidden="true" />
        </div>
      </div>
    </template>

    <!-- ─── main form ───────────────────────────────────────────────────────── -->
    <template v-else-if="loadState === 'ready'">

      <!-- Unconfigured guidance banner -->
      <div
        v-if="!savedSettings?.configured && selectedProvider === ''"
        class="guidance-banner"
        role="note"
        aria-label="AI 配置引导"
      >
        <div class="guidance-icon" aria-hidden="true">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 12h3.5l2.2-6 3.6 12 2.4-7 1.3 1h4.5"/>
            <circle cx="19" cy="5" r="2"/>
          </svg>
        </div>
        <div class="guidance-text">
          <strong>配置 LLM 以解锁 AI 诊断</strong>
          <p>接入 Claude、OpenAI 或本地 Ollama 后,流水线失败时 Pipewright 将自动生成根因假说与修复建议,无需手动排查日志。</p>
        </div>
      </div>

      <!-- Provider selection panel -->
      <div class="panel panel--anim">
        <div class="panel-head">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="10"/><path d="M12 8v4l3 3"/>
          </svg>
          选择提供商
          <span v-if="errors.provider" class="panel-head-error">{{ errors.provider }}</span>
        </div>
        <div class="panel-body">
          <div class="prov-grid" role="radiogroup" aria-label="AI 提供商选择">
            <button
              v-for="prov in PROVIDERS"
              :key="prov.id"
              type="button"
              class="prov-card"
              :class="{ 'prov-card--active': selectedProvider === prov.id }"
              :aria-pressed="selectedProvider === prov.id"
              :aria-label="`选择 ${prov.label}`"
              @click="selectProvider(prov.id)"
            >
              <span v-if="selectedProvider === prov.id" class="prov-check" aria-hidden="true">
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
                  <path d="m5 13 4 4 10-11"/>
                </svg>
              </span>
              <div class="prov-logo" :style="prov.logoStyle" aria-hidden="true">{{ prov.logoText }}</div>
              <div class="prov-name">{{ prov.label }}</div>
              <div class="prov-desc">{{ prov.desc }}</div>
              <span v-if="prov.tag" class="prov-tag" :style="prov.tagStyle">{{ prov.tag }}</span>
            </button>
          </div>
        </div>
      </div>

      <!-- Config detail panel (shown once a provider is chosen) -->
      <Transition name="panel-slide">
        <div v-if="selectedProvider !== ''" class="panel panel--anim" :key="selectedProvider">
          <!-- Panel header with provider logo -->
          <div class="panel-head">
            <div
              class="ph-logo"
              :style="PROVIDERS.find(p => p.id === selectedProvider)?.logoStyle"
              aria-hidden="true"
            >
              {{ PROVIDERS.find(p => p.id === selectedProvider)?.logoText }}
            </div>
            {{ PROVIDERS.find(p => p.id === selectedProvider)?.label }} 配置
            <span v-if="savedSettings?.updatedAt" class="panel-updated">
              上次保存 {{ relativeTime(savedSettings.updatedAt) }}
            </span>
          </div>
          <div class="panel-body">
            <form class="config-form" novalidate @submit.prevent="handleSave">

              <!-- API Key — only shown for non-Ollama -->
              <div v-if="!isOllama" class="config-row">
                <FormField
                  label="API Key"
                  field-id="ai-apikey"
                  :error="errors.apiKey"
                  hint="写入后仅显示掩码;留空则保留已存密钥"
                  :required="!hasExistingKey"
                >
                  <template #default="{ fieldId, ariaDescribedby }">
                    <div class="key-row">
                      <input
                        :id="fieldId"
                        v-model="apiKey"
                        type="password"
                        class="field-input field-input--mono"
                        :class="{ 'field-input--error': errors.apiKey }"
                        :placeholder="hasExistingKey
                          ? (isRotatingKey ? '正在替换…' : '已配置 ••••(留空不变)')
                          : '粘贴 API Key…'"
                        autocomplete="new-password"
                        :aria-invalid="!!errors.apiKey || undefined"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                        @input="errors.apiKey = ''"
                      />
                      <span
                        v-if="hasExistingKey && !isRotatingKey"
                        class="key-masked"
                        aria-label="`已配置掩码: ${savedSettings?.apiKeyMasked}`"
                      >
                        <span class="mono dim">{{ savedSettings?.apiKeyMasked }}</span>
                      </span>
                    </div>
                  </template>
                </FormField>
              </div>

              <!-- Ollama key-less hint -->
              <div v-else class="ollama-hint">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm0 4v4m0 4h.01"/>
                </svg>
                本地 Ollama 无需 API Key —— 确保 Ollama 服务已在指定地址运行即可。
              </div>

              <!-- Base URL -->
              <div class="config-row">
                <FormField
                  label="接入地址 (Base URL)"
                  field-id="ai-baseurl"
                  :error="errors.baseUrl"
                  :hint="`默认: ${DEFAULT_BASE_URLS[selectedProvider] ?? ''}`"
                  required
                >
                  <template #default="{ fieldId, ariaDescribedby }">
                    <input
                      :id="fieldId"
                      v-model="baseUrl"
                      type="url"
                      class="field-input field-input--mono"
                      :class="{ 'field-input--error': errors.baseUrl }"
                      :placeholder="DEFAULT_BASE_URLS[selectedProvider] ?? 'https://'"
                      autocomplete="off"
                      :aria-invalid="!!errors.baseUrl || undefined"
                      :aria-describedby="ariaDescribedby"
                      :disabled="saving"
                      @input="errors.baseUrl = ''"
                    />
                  </template>
                </FormField>
              </div>

              <!-- Model -->
              <div class="config-row">
                <FormField
                  label="模型"
                  field-id="ai-model"
                  hint="用于诊断的主模型,如 claude-opus-4-7 / gpt-4o / llama3"
                >
                  <template #default="{ fieldId, ariaDescribedby }">
                    <input
                      :id="fieldId"
                      v-model="model"
                      type="text"
                      class="field-input field-input--mono"
                      :placeholder="selectedProvider === 'claude'
                        ? 'claude-opus-4-7'
                        : selectedProvider === 'openai'
                          ? 'gpt-4o'
                          : 'llama3'"
                      autocomplete="off"
                      :aria-describedby="ariaDescribedby"
                      :disabled="saving"
                    />
                  </template>
                </FormField>
              </div>

              <!-- Test connection -->
              <div class="test-row">
                <AppButton
                  variant="ai"
                  type="button"
                  :loading="testState === 'running'"
                  :disabled="saving"
                  @click="handleTest"
                >
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                    <path d="M3 12h3.5l2.2-6 3.6 12 2.4-7 1.3 1h4.5"/>
                  </svg>
                  测试连接
                </AppButton>

                <!-- Test result -->
                <Transition name="fade">
                  <div
                    v-if="testState === 'ok' && testResult"
                    class="test-result test-result--ok"
                    role="status"
                  >
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" aria-hidden="true">
                      <path d="m5 13 4 4 10-11"/>
                    </svg>
                    连接正常 · 延迟 {{ testResult.latencyMs }}ms
                    <span v-if="testResult.detail" class="test-detail">· {{ testResult.detail }}</span>
                  </div>
                  <div
                    v-else-if="testState === 'fail' && testResult"
                    class="test-result test-result--fail"
                    role="alert"
                  >
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                      <circle cx="12" cy="12" r="9"/><path d="M15 9l-6 6M9 9l6 6"/>
                    </svg>
                    {{ testResult.error ?? '连接失败' }}
                    <span v-if="testResult.detail" class="test-detail">· {{ testResult.detail }}</span>
                  </div>
                </Transition>
              </div>

              <!-- Divider -->
              <div class="config-divider" aria-hidden="true" />

              <!-- Budget -->
              <div class="config-row">
                <FormField
                  label="月 Token 上限"
                  field-id="ai-budget"
                  hint="超出后暂停 AI 诊断(留空=不限制;本期仅声明,下一 Epic 强制执行)"
                >
                  <template #default="{ fieldId, ariaDescribedby }">
                    <div class="budget-row">
                      <input
                        :id="fieldId"
                        v-model="monthlyTokenLimit"
                        type="number"
                        min="0"
                        class="field-input budget-input"
                        placeholder="如 500000,留空不限制"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                      />
                    </div>
                  </template>
                </FormField>
              </div>

              <!-- Enabled toggle -->
              <div class="toggle-row">
                <button
                  type="button"
                  class="toggle-track"
                  :class="{ 'toggle-track--on': enabled }"
                  role="switch"
                  :aria-checked="enabled"
                  aria-label="启用 AI 功能"
                  :disabled="saving"
                  @click="enabled = !enabled"
                >
                  <span class="toggle-thumb" />
                </button>
                <div class="toggle-label">
                  <strong>启用 AI 功能</strong>
                  <span>关闭时 AI 诊断静默跳过,核心 CI/CD 流水线不受影响</span>
                </div>
              </div>

            </form>
          </div>
        </div>
      </Transition>

      <!-- Save bar (shown when dirty or provider selected) -->
      <Transition name="fade">
        <div v-if="isDirty || selectedProvider !== ''" class="save-bar">
          <span class="save-bar-note" :class="{ 'save-bar-note--warn': isDirty }">
            {{ isDirty ? '有未保存的改动' : '暂无改动' }}
          </span>
          <div class="save-bar-actions">
            <AppButton
              variant="ghost"
              type="button"
              :disabled="saving || !isDirty"
              @click="handleDiscard"
            >
              放弃
            </AppButton>
            <AppButton
              variant="primary"
              type="button"
              :loading="saving"
              :disabled="!isDirty"
              @click="handleSave"
            >
              保存更改
            </AppButton>
          </div>
        </div>
      </Transition>

    </template>
  </div>
</template>

<style scoped>
/* ─── layout ──────────────────────────────────────────────────────────────── */
.ai-root {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
}

/* ─── section header ──────────────────────────────────────────────────────── */
.section-head {
  display: flex;
  align-items: flex-start;
  gap: 16px;
}

.section-head-text {
  flex: 1;
}

.section-title {
  font-size: 1.12rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  color: var(--color-text);
}

.section-desc {
  font-size: 0.82rem;
  color: var(--color-faint);
  margin-top: 4px;
  max-width: 68ch;
  line-height: 1.55;
}

/* ─── status chip ─────────────────────────────────────────────────────────── */
.status-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border-radius: var(--rounded-full);
  font-size: 0.74rem;
  font-weight: 600;
  border: 1px solid;
  white-space: nowrap;
  flex-shrink: 0;
  margin-top: 3px;
}

.status-chip--ok {
  background: var(--color-green-soft);
  color: var(--color-green);
  border-color: var(--color-green-line);
}

.status-chip--idle {
  background: var(--color-inset);
  color: var(--color-faint);
  border-color: var(--color-border);
}

.status-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  background: var(--color-faint);
  flex-shrink: 0;
}

.status-dot--ok {
  background: var(--color-green);
  box-shadow: 0 0 0 3px var(--color-green-soft);
  animation: pulse-dot 2s ease-in-out infinite;
}

@keyframes pulse-dot {
  0%, 100% { box-shadow: 0 0 0 3px var(--color-green-soft); }
  50%       { box-shadow: 0 0 0 5px var(--color-green-soft); }
}

/* ─── banner ──────────────────────────────────────────────────────────────── */
.banner {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 11px 14px;
  border-radius: var(--rounded);
  font-size: 0.83rem;
  line-height: 1.5;
}

.banner--error {
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  color: var(--color-red);
}

.banner-retry {
  margin-left: auto;
  flex-shrink: 0;
  background: none;
  border: none;
  color: var(--color-red);
  font-size: 0.83rem;
  font-weight: 600;
  cursor: pointer;
  padding: 0;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.banner-retry:hover { opacity: 0.8; }

/* ─── guidance banner ─────────────────────────────────────────────────────── */
.guidance-banner {
  display: flex;
  gap: 14px;
  align-items: flex-start;
  padding: 16px 18px;
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  border-radius: var(--rounded-card);
  animation: panel-in 0.4s var(--ease-out-expo) both;
}

.guidance-icon {
  width: 40px;
  height: 40px;
  border-radius: var(--rounded-lg);
  background: var(--color-card);
  color: var(--color-cyan);
  display: grid;
  place-items: center;
  flex-shrink: 0;
  margin-top: 1px;
}

.guidance-text {
  flex: 1;
}

.guidance-text strong {
  font-size: 0.88rem;
  font-weight: 600;
  color: var(--color-text);
  display: block;
  margin-bottom: 4px;
}

.guidance-text p {
  font-size: 0.8rem;
  color: var(--color-dim);
  line-height: 1.55;
  margin: 0;
  max-width: 62ch;
}

/* ─── panel ───────────────────────────────────────────────────────────────── */
.panel {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
}

.panel--anim {
  animation: panel-in 0.45s var(--ease-out-expo) both;
}

@keyframes panel-in {
  from { opacity: 0; transform: translateY(13px); }
  to   { opacity: 1; transform: none; }
}

.panel-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 13px 18px;
  border-bottom: 1px solid var(--color-border);
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-text);
}

.panel-head-error {
  margin-left: auto;
  font-size: 0.76rem;
  font-weight: 500;
  color: var(--color-red);
}

.panel-updated {
  margin-left: auto;
  font-size: 0.72rem;
  font-weight: 400;
  color: var(--color-faint);
}

/* Provider header logo (small inline) */
.ph-logo {
  width: 22px;
  height: 22px;
  border-radius: var(--rounded-sm);
  display: grid;
  place-items: center;
  font-family: var(--font-mono);
  font-weight: 700;
  font-size: 0.75rem;
  flex-shrink: 0;
}

.panel-body {
  padding: 18px;
}

/* ─── skeleton ────────────────────────────────────────────────────────────── */
.skel-body {
  padding: 18px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.skel {
  display: block;
  background: linear-gradient(
    90deg,
    var(--color-inset) 0%,
    oklch(100% 0 0 / 0.06) 50%,
    var(--color-inset) 100%
  );
  background-size: 200% 100%;
  border-radius: var(--rounded-md);
  animation: shimmer 1.4s ease-in-out infinite;
}

.skel--text { height: 14px; }
.skel--field { width: 100%; }
.skel--card { height: 100px; border-radius: var(--rounded-card); }

@keyframes shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}

/* ─── provider card grid ──────────────────────────────────────────────────── */
.prov-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 10px;
}

.prov-card {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  padding: 14px 14px 12px;
  background: var(--color-inset);
  border: 1.5px solid var(--color-border);
  border-radius: var(--rounded-card);
  cursor: pointer;
  text-align: left;
  transition:
    border-color var(--duration-fast),
    background-color var(--duration-fast),
    transform var(--duration-fast),
    box-shadow var(--duration-fast);
}

.prov-card:hover:not(.prov-card--active) {
  border-color: var(--color-border-strong);
  transform: translateY(-2px);
  box-shadow: var(--shadow);
}

.prov-card:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.prov-card--active {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

.prov-check {
  position: absolute;
  top: 10px;
  right: 10px;
  width: 18px;
  height: 18px;
  border-radius: var(--rounded-full);
  background: var(--color-primary);
  color: #fff;
  display: grid;
  place-items: center;
}

.prov-logo {
  width: 34px;
  height: 34px;
  border-radius: var(--rounded-lg);
  display: grid;
  place-items: center;
  font-family: var(--font-mono);
  font-weight: 700;
  font-size: 1rem;
  flex-shrink: 0;
}

.prov-name {
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--color-text);
  margin-top: 4px;
}

.prov-desc {
  font-size: 0.74rem;
  color: var(--color-faint);
}

.prov-tag {
  margin-top: 3px;
  font-size: 0.68rem;
  font-weight: 600;
  padding: 2px 7px;
  border-radius: var(--rounded-full);
}

/* ─── config form ─────────────────────────────────────────────────────────── */
.config-form {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.config-row {
  /* No extra styling needed — FormField handles layout */
}

/* ─── field input ─────────────────────────────────────────────────────────── */
.field-input {
  width: 100%;
  height: 38px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  padding: 0 12px;
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 0.86rem;
  transition:
    border-color var(--duration-fast),
    box-shadow var(--duration-fast);
}

.field-input--mono {
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

.field-input::placeholder {
  color: var(--color-faint);
}

.field-input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

.field-input--error {
  border-color: var(--color-red) !important;
}

.field-input--error:focus {
  box-shadow: 0 0 0 3px var(--color-red-soft) !important;
}

.field-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* ─── key row / masked display ────────────────────────────────────────────── */
.key-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.key-masked {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding: 0 4px;
}

.dim {
  color: var(--color-faint);
}

.mono {
  font-family: var(--font-mono);
  font-size: 0.76rem;
  letter-spacing: 0.04em;
  user-select: none;
}

/* ─── ollama hint ─────────────────────────────────────────────────────────── */
.ollama-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 13px;
  background: var(--color-green-soft);
  border: 1px solid var(--color-green-line);
  border-radius: var(--rounded);
  font-size: 0.8rem;
  color: var(--color-dim);
  line-height: 1.5;
}

.ollama-hint svg {
  flex-shrink: 0;
  color: var(--color-green);
}

/* ─── test row ────────────────────────────────────────────────────────────── */
.test-row {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.test-result {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.8rem;
  font-weight: 500;
  padding: 7px 11px;
  border-radius: var(--rounded);
  line-height: 1.4;
}

.test-result--ok {
  background: var(--color-green-soft);
  color: var(--color-green);
  border: 1px solid var(--color-green-line);
}

.test-result--fail {
  background: var(--color-red-soft);
  color: var(--color-red);
  border: 1px solid var(--color-red-line);
}

.test-detail {
  color: inherit;
  opacity: 0.75;
}

/* ─── divider ─────────────────────────────────────────────────────────────── */
.config-divider {
  height: 1px;
  background: var(--color-border);
  margin: 2px 0;
}

/* ─── budget input ────────────────────────────────────────────────────────── */
.budget-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.budget-input {
  max-width: 220px;
}

/* ─── toggle ──────────────────────────────────────────────────────────────── */
.toggle-row {
  display: flex;
  align-items: flex-start;
  gap: 13px;
}

.toggle-track {
  position: relative;
  width: 40px;
  height: 22px;
  border-radius: var(--rounded-full);
  background: var(--color-border-strong);
  border: none;
  cursor: pointer;
  flex-shrink: 0;
  margin-top: 2px;
  transition: background-color var(--duration-normal);
}

.toggle-track:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.toggle-track:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.toggle-track--on {
  background: var(--color-primary);
}

.toggle-thumb {
  position: absolute;
  top: 3px;
  left: 3px;
  width: 16px;
  height: 16px;
  border-radius: var(--rounded-full);
  background: #fff;
  transition: transform var(--duration-normal) var(--ease-out-expo);
  pointer-events: none;
}

.toggle-track--on .toggle-thumb {
  transform: translateX(18px);
}

.toggle-label {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.toggle-label strong {
  font-size: 0.84rem;
  font-weight: 500;
  color: var(--color-text);
}

.toggle-label span {
  font-size: 0.76rem;
  color: var(--color-faint);
  line-height: 1.45;
}

/* ─── save bar ────────────────────────────────────────────────────────────── */
.save-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 13px 18px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
}

.save-bar-note {
  font-size: 0.78rem;
  color: var(--color-faint);
  flex: 1;
}

.save-bar-note--warn {
  color: var(--color-amber);
}

.save-bar-actions {
  display: flex;
  gap: 8px;
}

/* ─── panel-slide transition ──────────────────────────────────────────────── */
.panel-slide-enter-active {
  animation: panel-in 0.38s var(--ease-out-expo) both;
}

.panel-slide-leave-active {
  animation: panel-out 0.22s ease both;
}

@keyframes panel-out {
  from { opacity: 1; transform: none; }
  to   { opacity: 0; transform: translateY(-8px); }
}

/* ─── fade transition ─────────────────────────────────────────────────────── */
.fade-enter-active { transition: opacity var(--duration-normal), transform var(--duration-normal); }
.fade-leave-active { transition: opacity var(--duration-fast),  transform var(--duration-fast); }
.fade-enter-from  { opacity: 0; transform: translateY(6px); }
.fade-leave-to    { opacity: 0; transform: translateY(-4px); }

/* ─── reduced motion ──────────────────────────────────────────────────────── */
@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
  }
}
</style>
