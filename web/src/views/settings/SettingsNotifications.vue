<script setup lang="ts">
/**
 * SettingsNotifications (Story 5.1) — notification channels (FR-19).
 *
 * Delivers:
 *   - Channel list (name / type / enabled / config summary)
 *   - Create / edit (type webhook | email + per-type fields)
 *   - email SMTP password: WRITE-ONLY — never echoed; masked placeholder when stored
 *   - Test send with ok/latency/error result
 *   - Delete with confirm
 *   - wecom/dingtalk/feishu shown as "soon" placeholders (saveable, test=not_implemented)
 *
 * Reuses: FormField / AppButton tokens from 1-6. No new UI libraries.
 * Animation: transform/opacity + prefers-reduced-motion. Sensitive fields never
 * leave the input; the server returns only config.hasPassword.
 */
import { ref, computed, onMounted, reactive } from 'vue'
import FormField from '../../components/ui/FormField.vue'
import AppButton from '../../components/ui/AppButton.vue'
import { useToast } from '../../composables/useToast'
import { HttpError } from '../../api/http'
import {
  listChannels,
  createChannel,
  updateChannel,
  deleteChannel,
  testChannel,
  listRoutes,
  createRoute,
  deleteRoute,
  type ChannelType,
  type NotificationChannel,
  type ChannelConfigInput,
  type ChannelTestResult,
  type NotificationRoute,
  type NotificationEvent,
} from '../../api/notifications'

// ─── types / metadata ──────────────────────────────────────────────────────────

type LoadState = 'loading' | 'ready' | 'error'

interface TypeMeta {
  id: ChannelType
  label: string
  desc: string
  glyph: string
  glyphStyle: string
  implemented: boolean
}

const TYPES: TypeMeta[] = [
  {
    id: 'webhook',
    label: 'Webhook',
    desc: '自定义 HTTP POST',
    glyph: '{ }',
    glyphStyle: 'background:var(--color-primary-soft);color:var(--color-primary)',
    implemented: true,
  },
  {
    id: 'email',
    label: '邮件',
    desc: 'SMTP 发信',
    glyph: '@',
    glyphStyle: 'background:oklch(70% 0.14 250 / 0.16);color:oklch(62% 0.16 250)',
    implemented: true,
  },
  { id: 'wecom', label: '企业微信', desc: '即将支持', glyph: '企', glyphStyle: 'background:var(--color-inset);color:var(--color-faint)', implemented: false },
  { id: 'dingtalk', label: '钉钉', desc: '即将支持', glyph: '钉', glyphStyle: 'background:var(--color-inset);color:var(--color-faint)', implemented: false },
  { id: 'feishu', label: '飞书', desc: '即将支持', glyph: '飞', glyphStyle: 'background:var(--color-inset);color:var(--color-faint)', implemented: false },
]

function typeMeta(t: ChannelType): TypeMeta {
  return TYPES.find((m) => m.id === t) ?? TYPES[0]
}

const toast = useToast()

// ─── list state ──────────────────────────────────────────────────────────────

const loadState = ref<LoadState>('loading')
const loadError = ref('')
const channels = ref<NotificationChannel[]>([])

async function loadChannels(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    channels.value = await listChannels()
    loadState.value = 'ready'
  } catch (err) {
    loadError.value = httpMessage(err, '加载通知渠道失败,请稍后重试')
    loadState.value = 'error'
  }
}

onMounted(loadChannels)

// ─── editor state ──────────────────────────────────────────────────────────────

type EditorMode = 'closed' | 'create' | 'edit'
const editorMode = ref<EditorMode>('closed')
const editingId = ref<string | null>(null)

const form = reactive({
  name: '',
  type: 'webhook' as ChannelType,
  enabled: true,
  // webhook
  url: '',
  // email
  smtpHost: '',
  smtpPort: '' as string, // string for input; coerced on save
  from: '',
  to: '',
  username: '',
  password: '', // write-only: blank = keep existing on edit
})

/** Whether the channel being edited already has a stored SMTP password. */
const hasStoredPassword = ref(false)

const fieldErrors = reactive({
  name: '',
  url: '',
  smtpHost: '',
  smtpPort: '',
  from: '',
  to: '',
})

const saving = ref(false)

const isEmail = computed(() => form.type === 'email')
const isWebhook = computed(() => form.type === 'webhook')
const selectedTypeMeta = computed(() => typeMeta(form.type))

function resetForm(): void {
  form.name = ''
  form.type = 'webhook'
  form.enabled = true
  form.url = ''
  form.smtpHost = ''
  form.smtpPort = ''
  form.from = ''
  form.to = ''
  form.username = ''
  form.password = ''
  hasStoredPassword.value = false
  clearFieldErrors()
}

function clearFieldErrors(): void {
  fieldErrors.name = ''
  fieldErrors.url = ''
  fieldErrors.smtpHost = ''
  fieldErrors.smtpPort = ''
  fieldErrors.from = ''
  fieldErrors.to = ''
}

function openCreate(): void {
  resetForm()
  editingId.value = null
  editorMode.value = 'create'
}

function openEdit(ch: NotificationChannel): void {
  resetForm()
  editingId.value = ch.id
  form.name = ch.name
  form.type = ch.type
  form.enabled = ch.enabled
  form.url = ch.config.url ?? ''
  form.smtpHost = ch.config.smtpHost ?? ''
  form.smtpPort = ch.config.smtpPort != null ? String(ch.config.smtpPort) : ''
  form.from = ch.config.from ?? ''
  form.to = ch.config.to ?? ''
  form.username = ch.config.username ?? ''
  form.password = '' // never pre-fill
  hasStoredPassword.value = !!ch.config.hasPassword
  editorMode.value = 'edit'
}

function closeEditor(): void {
  editorMode.value = 'closed'
  editingId.value = null
}

function selectType(t: ChannelType): void {
  if (form.type === t) return
  form.type = t
  clearFieldErrors()
}

// ─── validation + save ─────────────────────────────────────────────────────────

function validate(): boolean {
  clearFieldErrors()
  let ok = true
  if (!form.name.trim()) {
    fieldErrors.name = '请填写渠道名称'
    ok = false
  }
  if (isWebhook.value) {
    if (!form.url.trim()) {
      fieldErrors.url = '请填写 Webhook 地址'
      ok = false
    }
  } else if (isEmail.value) {
    if (!form.smtpHost.trim()) {
      fieldErrors.smtpHost = '请填写 SMTP 主机'
      ok = false
    }
    const port = Number(form.smtpPort)
    if (!form.smtpPort.trim() || Number.isNaN(port) || port <= 0) {
      fieldErrors.smtpPort = '请填写有效端口'
      ok = false
    }
    if (!form.from.trim()) {
      fieldErrors.from = '请填写发件人'
      ok = false
    }
    if (!form.to.trim()) {
      fieldErrors.to = '请填写收件人'
      ok = false
    }
  }
  return ok
}

function buildConfig(): ChannelConfigInput {
  if (isWebhook.value) {
    return { url: form.url.trim() }
  }
  if (isEmail.value) {
    const cfg: ChannelConfigInput = {
      smtpHost: form.smtpHost.trim(),
      smtpPort: Number(form.smtpPort),
      from: form.from.trim(),
      to: form.to.trim(),
      username: form.username.trim(),
    }
    // Write-only: only send password when the user typed one (rotate).
    // On edit, blank means "keep existing" (omit). On create, blank = no password.
    if (form.password) cfg.password = form.password
    return cfg
  }
  // placeholder types: empty config accepted by API
  return {}
}

async function handleSave(): Promise<void> {
  if (!validate()) return
  saving.value = true
  try {
    const config = buildConfig()
    if (editorMode.value === 'edit' && editingId.value) {
      await updateChannel(editingId.value, {
        name: form.name.trim(),
        enabled: form.enabled,
        config,
      })
      toast.success('渠道已更新')
    } else {
      await createChannel({
        name: form.name.trim(),
        type: form.type,
        enabled: form.enabled,
        config,
      })
      toast.success('渠道已创建')
    }
    closeEditor()
    await loadChannels()
  } catch (err) {
    if (err instanceof HttpError && err.status === 422 && err.apiError) {
      mapValidationError(err.apiError.code, err.apiError.message)
    } else {
      toast.error('保存失败', { detail: httpMessage(err, '请稍后重试') })
    }
  } finally {
    saving.value = false
  }
}

function mapValidationError(code: string, message: string): void {
  switch (code) {
    case 'invalid_channel':
      fieldErrors.name = '渠道名称不能为空'
      break
    case 'url_not_allowed':
      fieldErrors.url = 'Webhook 地址不被允许:不可指向云元数据或链路本地地址'
      break
    case 'invalid_config':
      toast.error('保存失败', { detail: '渠道配置不完整或非法' })
      break
    default:
      toast.error('保存失败', { detail: message })
  }
}

// ─── delete ──────────────────────────────────────────────────────────────────

const deletingId = ref<string | null>(null)

async function handleDelete(ch: NotificationChannel): Promise<void> {
  if (!window.confirm(`确定删除渠道「${ch.name}」?此操作不可撤销。`)) return
  deletingId.value = ch.id
  try {
    await deleteChannel(ch.id)
    toast.success('渠道已删除')
    if (editingId.value === ch.id) closeEditor()
    await loadChannels()
  } catch (err) {
    toast.error('删除失败', { detail: httpMessage(err, '请稍后重试') })
  } finally {
    deletingId.value = null
  }
}

// ─── test send ─────────────────────────────────────────────────────────────────

const testingId = ref<string | null>(null)
const testResults = reactive<Record<string, ChannelTestResult>>({})

async function handleTest(ch: NotificationChannel): Promise<void> {
  testingId.value = ch.id
  delete testResults[ch.id]
  try {
    testResults[ch.id] = await testChannel(ch.id)
  } catch (err) {
    testResults[ch.id] = {
      ok: false,
      latencyMs: 0,
      detail: '',
      error: httpMessage(err, '测试发送失败'),
    }
  } finally {
    testingId.value = null
  }
}

// ─── event routes (Story 5.2 / FR-20) ─────────────────────────────────────────
// 「事件 → 渠道」映射区:独立于上方渠道 CRUD;未配路由的事件不发送。

interface EventMeta {
  id: NotificationEvent
  label: string
  desc: string
}

const EVENTS: EventMeta[] = [
  { id: 'build_succeeded', label: '构建成功', desc: '运行成功完成' },
  { id: 'build_failed', label: '构建失败', desc: '运行失败' },
  { id: 'deploy_succeeded', label: '部署成功', desc: '部署完成' },
  { id: 'deploy_failed', label: '部署失败', desc: '部分目标失败' },
  { id: 'rollback', label: '回滚', desc: '已回滚到上一版本' },
  { id: 'health_check_failed', label: '健康检查失败', desc: '部署后健康检查未通过' },
]

const routesLoadState = ref<LoadState>('loading')
const routesLoadError = ref('')
const routes = ref<NotificationRoute[]>([])

async function loadRoutes(): Promise<void> {
  routesLoadState.value = 'loading'
  routesLoadError.value = ''
  try {
    routes.value = await listRoutes()
    routesLoadState.value = 'ready'
  } catch (err) {
    routesLoadError.value = httpMessage(err, '加载事件路由失败,请稍后重试')
    routesLoadState.value = 'error'
  }
}

/** Routes grouped by event id (preserves server order within each group). */
const routesByEvent = computed<Record<string, NotificationRoute[]>>(() => {
  const map: Record<string, NotificationRoute[]> = {}
  for (const e of EVENTS) map[e.id] = []
  for (const r of routes.value) {
    if (!map[r.event]) map[r.event] = []
    map[r.event].push(r)
  }
  return map
})

/** Channel name lookup for rendering route rows. */
function channelName(channelId: string): string {
  return channels.value.find((c) => c.id === channelId)?.name ?? '(已删除渠道)'
}

// Add-route form state, keyed per event so each event row has its own picker.
const addingEvent = ref<NotificationEvent | null>(null)
const addChannelId = ref('')
const savingRoute = ref(false)

function openAddRoute(event: NotificationEvent): void {
  addingEvent.value = event
  // Default to first channel not already routed for this event.
  const used = new Set((routesByEvent.value[event] ?? []).map((r) => r.channelId))
  const free = channels.value.find((c) => !used.has(c.id))
  addChannelId.value = free?.id ?? channels.value[0]?.id ?? ''
}

function cancelAddRoute(): void {
  addingEvent.value = null
  addChannelId.value = ''
}

async function handleAddRoute(): Promise<void> {
  if (!addingEvent.value || !addChannelId.value) return
  savingRoute.value = true
  try {
    await createRoute({ event: addingEvent.value, channelId: addChannelId.value, enabled: true })
    toast.success('已添加事件路由')
    cancelAddRoute()
    await loadRoutes()
  } catch (err) {
    if (err instanceof HttpError && err.status === 422 && err.apiError) {
      toast.error('添加失败', { detail: err.apiError.message })
    } else {
      toast.error('添加失败', { detail: httpMessage(err, '请稍后重试') })
    }
  } finally {
    savingRoute.value = false
  }
}

const deletingRouteId = ref<string | null>(null)

async function handleDeleteRoute(route: NotificationRoute): Promise<void> {
  deletingRouteId.value = route.id
  try {
    await deleteRoute(route.id)
    toast.success('已移除事件路由')
    await loadRoutes()
  } catch (err) {
    toast.error('移除失败', { detail: httpMessage(err, '请稍后重试') })
  } finally {
    deletingRouteId.value = null
  }
}

onMounted(loadRoutes)

// ─── helpers ─────────────────────────────────────────────────────────────────

function httpMessage(err: unknown, fallback: string): string {
  if (err instanceof HttpError) {
    if (err.status === 0) return '无法连接到服务器,请检查后端是否运行'
    return err.apiError?.message ?? `请求失败(${err.status})`
  }
  return err instanceof Error ? err.message : fallback
}

function configSummary(ch: NotificationChannel): string {
  if (ch.type === 'webhook') return ch.config.url ?? ''
  if (ch.type === 'email') {
    const port = ch.config.smtpPort ? `:${ch.config.smtpPort}` : ''
    return `${ch.config.smtpHost ?? ''}${port} → ${ch.config.to ?? ''}`
  }
  return '即将支持'
}
</script>

<template>
  <div class="nt-root">
    <!-- ─── section header ──────────────────────────────────────────────────── -->
    <div class="section-head">
      <div class="section-head-text">
        <h2 class="section-title">通知渠道</h2>
        <p class="section-desc">
          配置 Webhook 与邮件等渠道,构建/部署事件将经这些渠道通知你。敏感字段(如 SMTP 密码)仅以密文存于本实例的加密保险库,绝不回显。
        </p>
      </div>
      <AppButton
        v-if="loadState === 'ready' && editorMode === 'closed'"
        variant="primary"
        type="button"
        @click="openCreate"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
          <path d="M12 5v14M5 12h14" />
        </svg>
        新增渠道
      </AppButton>
    </div>

    <!-- ─── load error ──────────────────────────────────────────────────────── -->
    <div v-if="loadState === 'error'" class="banner banner--error" role="alert">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
      </svg>
      <span>{{ loadError }}</span>
      <button class="banner-retry" @click="loadChannels">↻ 重试</button>
    </div>

    <!-- ─── loading skeleton ────────────────────────────────────────────────── -->
    <template v-if="loadState === 'loading'">
      <div class="panel panel--anim">
        <div class="skel-body">
          <div v-for="i in 2" :key="i" class="skel skel--row" aria-hidden="true" />
        </div>
      </div>
    </template>

    <!-- ─── main ────────────────────────────────────────────────────────────── -->
    <template v-else-if="loadState === 'ready'">

      <!-- Editor panel (create / edit) -->
      <Transition name="panel-slide">
        <div v-if="editorMode !== 'closed'" class="panel panel--anim editor">
          <div class="panel-head">
            <div class="ph-glyph" :style="selectedTypeMeta.glyphStyle" aria-hidden="true">
              {{ selectedTypeMeta.glyph }}
            </div>
            {{ editorMode === 'edit' ? '编辑渠道' : '新增渠道' }}
            <button class="editor-close" type="button" aria-label="关闭" @click="closeEditor">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
            </button>
          </div>
          <div class="panel-body">
            <form class="config-form" novalidate @submit.prevent="handleSave">

              <!-- Type selector (locked when editing — type is immutable) -->
              <div class="config-row">
                <span class="field-label">渠道类型</span>
                <div class="type-grid" role="radiogroup" aria-label="渠道类型">
                  <button
                    v-for="m in TYPES"
                    :key="m.id"
                    type="button"
                    class="type-card"
                    :class="{ 'type-card--active': form.type === m.id, 'type-card--soon': !m.implemented }"
                    :aria-pressed="form.type === m.id"
                    :disabled="editorMode === 'edit' && form.type !== m.id"
                    @click="selectType(m.id)"
                  >
                    <span class="type-glyph" :style="m.glyphStyle" aria-hidden="true">{{ m.glyph }}</span>
                    <span class="type-name">{{ m.label }}</span>
                    <span class="type-desc">{{ m.desc }}</span>
                    <span v-if="!m.implemented" class="type-soon">未实现发送</span>
                  </button>
                </div>
              </div>

              <!-- Name -->
              <div class="config-row">
                <FormField label="渠道名称" field-id="nt-name" :error="fieldErrors.name" required>
                  <template #default="{ fieldId, ariaDescribedby }">
                    <input
                      :id="fieldId"
                      v-model="form.name"
                      type="text"
                      class="field-input"
                      :class="{ 'field-input--error': fieldErrors.name }"
                      placeholder="如:运维群 webhook"
                      :aria-describedby="ariaDescribedby"
                      :disabled="saving"
                      @input="fieldErrors.name = ''"
                    />
                  </template>
                </FormField>
              </div>

              <!-- Webhook fields -->
              <div v-if="isWebhook" class="config-row">
                <FormField
                  label="Webhook 地址"
                  field-id="nt-url"
                  :error="fieldErrors.url"
                  hint="POST JSON 到该地址;不可指向云元数据/链路本地,允许私网自托管"
                  required
                >
                  <template #default="{ fieldId, ariaDescribedby }">
                    <input
                      :id="fieldId"
                      v-model="form.url"
                      type="url"
                      class="field-input field-input--mono"
                      :class="{ 'field-input--error': fieldErrors.url }"
                      placeholder="https://hooks.example.com/..."
                      :aria-describedby="ariaDescribedby"
                      :disabled="saving"
                      @input="fieldErrors.url = ''"
                    />
                  </template>
                </FormField>
              </div>

              <!-- Email fields -->
              <template v-else-if="isEmail">
                <div class="config-grid">
                  <FormField label="SMTP 主机" field-id="nt-host" :error="fieldErrors.smtpHost" required>
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.smtpHost"
                        type="text"
                        class="field-input field-input--mono"
                        :class="{ 'field-input--error': fieldErrors.smtpHost }"
                        placeholder="smtp.example.com"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                        @input="fieldErrors.smtpHost = ''"
                      />
                    </template>
                  </FormField>
                  <FormField label="端口" field-id="nt-port" :error="fieldErrors.smtpPort" required>
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.smtpPort"
                        type="number"
                        min="1"
                        class="field-input field-input--mono"
                        :class="{ 'field-input--error': fieldErrors.smtpPort }"
                        placeholder="587"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                        @input="fieldErrors.smtpPort = ''"
                      />
                    </template>
                  </FormField>
                </div>
                <div class="config-grid">
                  <FormField label="发件人" field-id="nt-from" :error="fieldErrors.from" required>
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.from"
                        type="email"
                        class="field-input field-input--mono"
                        :class="{ 'field-input--error': fieldErrors.from }"
                        placeholder="ci@example.com"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                        @input="fieldErrors.from = ''"
                      />
                    </template>
                  </FormField>
                  <FormField label="收件人" field-id="nt-to" :error="fieldErrors.to" hint="多个用逗号分隔" required>
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.to"
                        type="text"
                        class="field-input field-input--mono"
                        :class="{ 'field-input--error': fieldErrors.to }"
                        placeholder="ops@example.com, dev@example.com"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                        @input="fieldErrors.to = ''"
                      />
                    </template>
                  </FormField>
                </div>
                <div class="config-grid">
                  <FormField label="用户名" field-id="nt-user" hint="留空默认用发件人地址">
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.username"
                        type="text"
                        class="field-input field-input--mono"
                        placeholder="同发件人"
                        autocomplete="off"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                      />
                    </template>
                  </FormField>
                  <FormField
                    label="SMTP 密码"
                    field-id="nt-pw"
                    :hint="hasStoredPassword ? '已配置 ••••(留空则保留)' : '写入后仅显示掩码,绝不回显'"
                  >
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.password"
                        type="password"
                        class="field-input field-input--mono"
                        :placeholder="hasStoredPassword ? '已配置 ••••(留空不变)' : '粘贴 SMTP 密码…'"
                        autocomplete="new-password"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                      />
                    </template>
                  </FormField>
                </div>
              </template>

              <!-- Placeholder type hint -->
              <div v-else class="soon-hint" role="note">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
                </svg>
                该渠道类型本期可保存配置,但发送功能尚未实现(测试将返回 not_implemented),敬请后续版本支持。
              </div>

              <!-- Enabled toggle -->
              <div class="toggle-row">
                <button
                  type="button"
                  class="toggle-track"
                  :class="{ 'toggle-track--on': form.enabled }"
                  role="switch"
                  :aria-checked="form.enabled"
                  aria-label="启用该渠道"
                  :disabled="saving"
                  @click="form.enabled = !form.enabled"
                >
                  <span class="toggle-thumb" />
                </button>
                <div class="toggle-label">
                  <strong>启用渠道</strong>
                  <span>停用后该渠道不发送,但配置保留</span>
                </div>
              </div>

              <!-- Save bar -->
              <div class="editor-actions">
                <AppButton variant="ghost" type="button" :disabled="saving" @click="closeEditor">取消</AppButton>
                <AppButton variant="primary" type="submit" :loading="saving">
                  {{ editorMode === 'edit' ? '保存更改' : '创建渠道' }}
                </AppButton>
              </div>
            </form>
          </div>
        </div>
      </Transition>

      <!-- Empty state -->
      <div v-if="channels.length === 0 && editorMode === 'closed'" class="empty-state">
        <div class="empty-glyph" aria-hidden="true">
          <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9M13.73 21a2 2 0 0 1-3.46 0" />
          </svg>
        </div>
        <strong>尚未配置通知渠道</strong>
        <p>新增一个 Webhook 或邮件渠道,构建/部署事件即可经它通知你。</p>
        <AppButton variant="primary" type="button" @click="openCreate">新增渠道</AppButton>
      </div>

      <!-- Channel list -->
      <ul v-else-if="channels.length > 0" class="ch-list">
        <li v-for="ch in channels" :key="ch.id" class="ch-card panel--anim">
          <div class="ch-main">
            <div class="ch-glyph" :style="typeMeta(ch.type).glyphStyle" aria-hidden="true">
              {{ typeMeta(ch.type).glyph }}
            </div>
            <div class="ch-text">
              <div class="ch-name-row">
                <span class="ch-name">{{ ch.name }}</span>
                <span class="ch-type-tag">{{ typeMeta(ch.type).label }}</span>
                <span
                  class="ch-status"
                  :class="ch.enabled ? 'ch-status--on' : 'ch-status--off'"
                >
                  <span class="ch-dot" :class="{ 'ch-dot--on': ch.enabled }" aria-hidden="true" />
                  {{ ch.enabled ? '已启用' : '已停用' }}
                </span>
              </div>
              <div class="ch-summary mono">{{ configSummary(ch) }}</div>

              <!-- Test result -->
              <Transition name="fade">
                <div
                  v-if="testResults[ch.id]"
                  class="test-result"
                  :class="testResults[ch.id].ok ? 'test-result--ok' : 'test-result--fail'"
                  :role="testResults[ch.id].ok ? 'status' : 'alert'"
                >
                  <template v-if="testResults[ch.id].ok">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" aria-hidden="true">
                      <path d="m5 13 4 4 10-11" />
                    </svg>
                    发送成功 · {{ testResults[ch.id].latencyMs }}ms
                    <span v-if="testResults[ch.id].detail" class="test-detail">· {{ testResults[ch.id].detail }}</span>
                  </template>
                  <template v-else>
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                      <circle cx="12" cy="12" r="9" /><path d="M15 9l-6 6M9 9l6 6" />
                    </svg>
                    {{ testResults[ch.id].error ?? '发送失败' }}
                  </template>
                </div>
              </Transition>
            </div>
          </div>
          <div class="ch-actions">
            <AppButton
              variant="default"
              type="button"
              :loading="testingId === ch.id"
              @click="handleTest(ch)"
            >
              测试发送
            </AppButton>
            <button class="icon-btn" type="button" aria-label="编辑" @click="openEdit(ch)">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
              </svg>
            </button>
            <button
              class="icon-btn icon-btn--danger"
              type="button"
              aria-label="删除"
              :disabled="deletingId === ch.id"
              @click="handleDelete(ch)"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m2 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
              </svg>
            </button>
          </div>
        </li>
      </ul>

    </template>

    <!-- ─── event routes (Story 5.2 / FR-20) ─────────────────────────────────── -->
    <section class="routes-section" aria-labelledby="routes-heading">
      <div class="section-head">
        <div class="section-head-text">
          <h2 id="routes-heading" class="section-title">事件路由</h2>
          <p class="section-desc">
            配置「哪些事件经哪些渠道通知」。<strong>未配置路由的事件不会发送任何通知</strong>,你只在关心的事件上被打扰。一个事件可路由到多个渠道。
          </p>
        </div>
      </div>

      <!-- routes load error -->
      <div v-if="routesLoadState === 'error'" class="banner banner--error" role="alert">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
        </svg>
        <span>{{ routesLoadError }}</span>
        <button class="banner-retry" @click="loadRoutes">↻ 重试</button>
      </div>

      <!-- routes loading -->
      <div v-else-if="routesLoadState === 'loading'" class="panel panel--anim">
        <div class="skel-body">
          <div v-for="i in 3" :key="i" class="skel skel--row" aria-hidden="true" />
        </div>
      </div>

      <!-- no channels: routes need a channel first -->
      <div v-else-if="channels.length === 0" class="soon-hint" role="note">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
        </svg>
        先在上方新增至少一个通知渠道,才能为事件配置路由。
      </div>

      <!-- event → channel mapping table -->
      <ul v-else class="event-list">
        <li v-for="ev in EVENTS" :key="ev.id" class="event-card panel--anim">
          <div class="event-head">
            <div class="event-text">
              <span class="event-name">{{ ev.label }}</span>
              <span class="event-code mono">{{ ev.id }}</span>
              <span class="event-desc">{{ ev.desc }}</span>
            </div>
            <AppButton
              v-if="addingEvent !== ev.id"
              variant="default"
              type="button"
              @click="openAddRoute(ev.id)"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                <path d="M12 5v14M5 12h14" />
              </svg>
              添加渠道
            </AppButton>
          </div>

          <!-- routed channel chips -->
          <div v-if="routesByEvent[ev.id] && routesByEvent[ev.id].length > 0" class="route-chips">
            <span v-for="r in routesByEvent[ev.id]" :key="r.id" class="route-chip">
              <span class="route-chip-dot" :class="{ 'route-chip-dot--on': r.enabled }" aria-hidden="true" />
              {{ channelName(r.channelId) }}
              <button
                class="route-chip-x"
                type="button"
                :aria-label="`移除 ${channelName(r.channelId)}`"
                :disabled="deletingRouteId === r.id"
                @click="handleDeleteRoute(r)"
              >
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                  <path d="M18 6 6 18M6 6l12 12" />
                </svg>
              </button>
            </span>
          </div>
          <div v-else-if="addingEvent !== ev.id" class="route-empty">未配置 · 该事件不通知</div>

          <!-- add-route inline picker -->
          <div v-if="addingEvent === ev.id" class="route-add">
            <select v-model="addChannelId" class="route-select" :disabled="savingRoute" aria-label="选择渠道">
              <option v-for="c in channels" :key="c.id" :value="c.id">
                {{ c.name }}{{ c.enabled ? '' : '(已停用)' }}
              </option>
            </select>
            <AppButton variant="primary" type="button" :loading="savingRoute" :disabled="!addChannelId" @click="handleAddRoute">
              添加
            </AppButton>
            <AppButton variant="ghost" type="button" :disabled="savingRoute" @click="cancelAddRoute">取消</AppButton>
          </div>
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.nt-root {
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

.section-head-text { flex: 1; }

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
  max-width: 70ch;
  line-height: 1.55;
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

.ph-glyph {
  width: 24px;
  height: 24px;
  border-radius: var(--rounded-sm);
  display: grid;
  place-items: center;
  font-family: var(--font-mono);
  font-weight: 700;
  font-size: 0.72rem;
  flex-shrink: 0;
}

.editor-close {
  margin-left: auto;
  background: none;
  border: none;
  color: var(--color-faint);
  cursor: pointer;
  display: grid;
  place-items: center;
  padding: 4px;
  border-radius: var(--rounded-sm);
  transition: color var(--duration-fast), background-color var(--duration-fast);
}

.editor-close:hover { color: var(--color-text); background: var(--color-inset); }

.panel-body { padding: 18px; }

/* ─── skeleton ────────────────────────────────────────────────────────────── */
.skel-body {
  padding: 18px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.skel {
  display: block;
  background: linear-gradient(90deg, var(--color-inset) 0%, oklch(100% 0 0 / 0.06) 50%, var(--color-inset) 100%);
  background-size: 200% 100%;
  border-radius: var(--rounded-md);
  animation: shimmer 1.4s ease-in-out infinite;
}

.skel--row { height: 56px; }

@keyframes shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}

/* ─── config form ─────────────────────────────────────────────────────────── */
.config-form {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.config-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}

.field-label {
  display: block;
  font-size: 0.78rem;
  font-weight: 500;
  color: var(--color-dim);
  margin-bottom: 8px;
}

/* ─── type selector ───────────────────────────────────────────────────────── */
.type-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(110px, 1fr));
  gap: 10px;
}

.type-card {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  padding: 11px 12px;
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

.type-card:hover:not(.type-card--active):not(:disabled) {
  border-color: var(--color-border-strong);
  transform: translateY(-2px);
  box-shadow: var(--shadow);
}

.type-card:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.type-card--active {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

.type-card:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.type-glyph {
  width: 28px;
  height: 28px;
  border-radius: var(--rounded-md);
  display: grid;
  place-items: center;
  font-family: var(--font-mono);
  font-weight: 700;
  font-size: 0.82rem;
  flex-shrink: 0;
}

.type-name {
  font-size: 0.84rem;
  font-weight: 600;
  color: var(--color-text);
  margin-top: 3px;
}

.type-desc {
  font-size: 0.7rem;
  color: var(--color-faint);
}

.type-soon {
  font-size: 0.64rem;
  color: var(--color-amber);
  font-weight: 600;
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
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}

.field-input--mono {
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

.field-input::placeholder { color: var(--color-faint); }

.field-input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

.field-input--error { border-color: var(--color-red) !important; }
.field-input--error:focus { box-shadow: 0 0 0 3px var(--color-red-soft) !important; }
.field-input:disabled { opacity: 0.5; cursor: not-allowed; }

/* ─── soon hint ───────────────────────────────────────────────────────────── */
.soon-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 13px;
  background: var(--color-amber-soft, var(--color-inset));
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  font-size: 0.8rem;
  color: var(--color-dim);
  line-height: 1.5;
}

.soon-hint svg { flex-shrink: 0; color: var(--color-amber); }

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

.toggle-track:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.toggle-track:disabled { opacity: 0.4; cursor: not-allowed; }
.toggle-track--on { background: var(--color-primary); }

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

.toggle-track--on .toggle-thumb { transform: translateX(18px); }

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

/* ─── editor actions ──────────────────────────────────────────────────────── */
.editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 4px;
  border-top: 1px solid var(--color-border);
  margin-top: 2px;
  padding-top: 16px;
}

/* ─── empty state ─────────────────────────────────────────────────────────── */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 48px 24px;
  text-align: center;
  background: var(--color-card);
  border: 1px dashed var(--color-border-strong);
  border-radius: var(--rounded-card);
}

.empty-glyph {
  width: 52px;
  height: 52px;
  border-radius: var(--rounded-lg);
  background: var(--color-inset);
  color: var(--color-faint);
  display: grid;
  place-items: center;
  margin-bottom: 4px;
}

.empty-state strong {
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--color-text);
}

.empty-state p {
  font-size: 0.82rem;
  color: var(--color-faint);
  max-width: 42ch;
  line-height: 1.5;
  margin: 0 0 6px;
}

/* ─── channel list ────────────────────────────────────────────────────────── */
.ch-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 0;
  margin: 0;
}

.ch-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 14px 16px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}

.ch-card:hover {
  border-color: var(--color-border-strong);
}

.ch-main {
  display: flex;
  align-items: flex-start;
  gap: 13px;
  min-width: 0;
  flex: 1;
}

.ch-glyph {
  width: 38px;
  height: 38px;
  border-radius: var(--rounded-lg);
  display: grid;
  place-items: center;
  font-family: var(--font-mono);
  font-weight: 700;
  font-size: 0.92rem;
  flex-shrink: 0;
}

.ch-text { min-width: 0; flex: 1; }

.ch-name-row {
  display: flex;
  align-items: center;
  gap: 9px;
  flex-wrap: wrap;
}

.ch-name {
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--color-text);
}

.ch-type-tag {
  font-size: 0.68rem;
  font-weight: 600;
  padding: 2px 7px;
  border-radius: var(--rounded-full);
  background: var(--color-inset);
  color: var(--color-faint);
}

.ch-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 0.7rem;
  font-weight: 600;
}

.ch-status--on { color: var(--color-green); }
.ch-status--off { color: var(--color-faint); }

.ch-dot {
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  background: var(--color-faint);
}

.ch-dot--on {
  background: var(--color-green);
  box-shadow: 0 0 0 3px var(--color-green-soft);
}

.ch-summary {
  font-size: 0.76rem;
  color: var(--color-faint);
  margin-top: 4px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 52ch;
}

.mono {
  font-family: var(--font-mono);
}

.ch-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.icon-btn {
  width: 32px;
  height: 32px;
  display: grid;
  place-items: center;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  color: var(--color-dim);
  cursor: pointer;
  transition: color var(--duration-fast), border-color var(--duration-fast), background-color var(--duration-fast);
}

.icon-btn:hover {
  color: var(--color-text);
  border-color: var(--color-border-strong);
}

.icon-btn--danger:hover {
  color: var(--color-red);
  border-color: var(--color-red-line);
  background: var(--color-red-soft);
}

.icon-btn:disabled { opacity: 0.4; cursor: not-allowed; }

/* ─── test result ─────────────────────────────────────────────────────────── */
.test-result {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.76rem;
  font-weight: 500;
  padding: 5px 9px;
  border-radius: var(--rounded);
  line-height: 1.4;
  margin-top: 8px;
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

.test-detail { opacity: 0.75; }

/* ─── transitions ─────────────────────────────────────────────────────────── */
.panel-slide-enter-active { animation: panel-in 0.38s var(--ease-out-expo) both; }
.panel-slide-leave-active { animation: panel-out 0.22s ease both; }

@keyframes panel-out {
  from { opacity: 1; transform: none; }
  to   { opacity: 0; transform: translateY(-8px); }
}

.fade-enter-active { transition: opacity var(--duration-normal), transform var(--duration-normal); }
.fade-leave-active { transition: opacity var(--duration-fast), transform var(--duration-fast); }
.fade-enter-from { opacity: 0; transform: translateY(6px); }
.fade-leave-to { opacity: 0; transform: translateY(-4px); }

/* ─── event routes (Story 5.2) ────────────────────────────────────────────── */
.routes-section {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
  margin-top: 8px;
  padding-top: var(--card-gap);
  border-top: 1px solid var(--color-border);
}

.event-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 0;
  margin: 0;
}

.event-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  transition: border-color var(--duration-fast);
}

.event-card:hover { border-color: var(--color-border-strong); }

.event-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.event-text {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
}

.event-name {
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--color-text);
}

.event-code {
  font-size: 0.68rem;
  color: var(--color-faint);
  background: var(--color-inset);
  padding: 1px 6px;
  border-radius: var(--rounded-full);
}

.event-desc {
  font-size: 0.74rem;
  color: var(--color-faint);
}

.route-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
}

.route-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.78rem;
  font-weight: 500;
  color: var(--color-text);
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-full);
  padding: 4px 6px 4px 10px;
}

.route-chip-dot {
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  background: var(--color-faint);
  flex-shrink: 0;
}

.route-chip-dot--on {
  background: var(--color-green);
  box-shadow: 0 0 0 3px var(--color-green-soft);
}

.route-chip-x {
  display: grid;
  place-items: center;
  width: 18px;
  height: 18px;
  border: none;
  border-radius: var(--rounded-full);
  background: transparent;
  color: var(--color-faint);
  cursor: pointer;
  transition: color var(--duration-fast), background-color var(--duration-fast);
}

.route-chip-x:hover { color: var(--color-red); background: var(--color-red-soft); }
.route-chip-x:disabled { opacity: 0.4; cursor: not-allowed; }

.route-empty {
  font-size: 0.76rem;
  color: var(--color-faint);
  font-style: italic;
}

.route-add {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.route-select {
  height: 36px;
  min-width: 200px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  padding: 0 10px;
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 0.84rem;
  cursor: pointer;
}

.route-select:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

/* ─── reduced motion ──────────────────────────────────────────────────────── */
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
  }
}
</style>
