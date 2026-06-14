<script setup lang="ts">
/**
 * SettingsNotifications (Story 5.1) — notification channels (FR-19).
 *
 * Delivers:
 *   - Channel list (name / type / enabled / config summary)
 *   - Create / edit (type webhook | email | feishu + per-type fields)
 *   - email SMTP password: WRITE-ONLY — never echoed; masked placeholder when stored
 *   - Test send with ok/latency/error result
 *   - Delete with confirm
 *   - feishu: custom-bot webhook (url + optional sign secret); sends interactive card
 *   - wecom: group-robot webhook (url only); sends markdown
 *   - dingtalk: group-robot webhook (url + optional sign secret); sends markdown
 *
 * Reuses: FormField / AppButton tokens from 1-6. No new UI libraries.
 * Animation: transform/opacity + prefers-reduced-motion. Sensitive fields never
 * leave the input; the server returns only config.hasPassword.
 */
import { ref, computed, onMounted, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
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
  listTemplates,
  createTemplate,
  updateTemplate,
  deleteTemplate,
  getNotifyConfig,
  setNotifyConfig,
  TEMPLATE_VARIABLES,
  type ChannelType,
  type NotificationChannel,
  type ChannelConfigInput,
  type ChannelTestResult,
  type NotificationRoute,
  type NotificationEvent,
  type NotificationTemplate,
} from '../../api/notifications'
import { listProjects, type Project } from '../../api/projects'
import { SUPPORTED_LOCALES } from '../../i18n'

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

const { t } = useI18n()
const toast = useToast()

// 通知语言:外发通知(飞书/邮件)默认文案的语言,与界面语言解耦。
const notifyLang = ref('zh-CN')
const localeOptions = SUPPORTED_LOCALES
async function loadNotifyConfig(): Promise<void> {
  try {
    const cfg = await getNotifyConfig()
    if (cfg.language) notifyLang.value = cfg.language
  } catch {
    // 非致命:保持默认,不打断渠道页加载。
  }
}
async function onNotifyLangChange(): Promise<void> {
  try {
    await setNotifyConfig(notifyLang.value)
    toast.success(t('settingsNotifications.langSaved'))
  } catch (err) {
    toast.error(err instanceof HttpError ? err.message : t('settingsNotifications.langSaveFailed'))
  }
}
onMounted(loadNotifyConfig)

const TYPES = computed<TypeMeta[]>(() => [
  {
    id: 'webhook',
    label: 'Webhook',
    desc: t('settingsNotifications.typeWebhookDesc'),
    glyph: '{ }',
    glyphStyle: 'background:var(--color-primary-soft);color:var(--color-primary)',
    implemented: true,
  },
  {
    id: 'email',
    label: t('settingsNotifications.typeEmailLabel'),
    desc: t('settingsNotifications.typeEmailDesc'),
    glyph: '@',
    glyphStyle: 'background:oklch(70% 0.14 250 / 0.16);color:oklch(62% 0.16 250)',
    implemented: true,
  },
  {
    id: 'feishu',
    label: t('settingsNotifications.typeFeishuLabel'),
    desc: t('settingsNotifications.typeFeishuDesc'),
    glyph: '飞',
    glyphStyle: 'background:oklch(72% 0.15 200 / 0.16);color:oklch(56% 0.13 220)',
    implemented: true,
  },
  {
    id: 'wecom',
    label: t('settingsNotifications.typeWecomLabel'),
    desc: t('settingsNotifications.typeWecomDesc'),
    glyph: '企',
    glyphStyle: 'background:oklch(72% 0.16 145 / 0.16);color:oklch(52% 0.15 150)',
    implemented: true,
  },
  {
    id: 'dingtalk',
    label: t('settingsNotifications.typeDingtalkLabel'),
    desc: t('settingsNotifications.typeDingtalkDesc'),
    glyph: '钉',
    glyphStyle: 'background:oklch(70% 0.15 250 / 0.16);color:oklch(54% 0.16 255)',
    implemented: true,
  },
])

function typeMeta(ty: ChannelType): TypeMeta {
  return TYPES.value.find((m) => m.id === ty) ?? TYPES.value[0]
}

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
    loadError.value = httpMessage(err, t('settingsNotifications.errLoadChannels'))
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
const isFeishu = computed(() => form.type === 'feishu')
const isWecom = computed(() => form.type === 'wecom')
const isDingtalk = computed(() => form.type === 'dingtalk')
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
    fieldErrors.name = t('settingsNotifications.valNameRequired')
    ok = false
  }
  if (isWebhook.value) {
    if (!form.url.trim()) {
      fieldErrors.url = t('settingsNotifications.valWebhookUrlRequired')
      ok = false
    }
  } else if (isFeishu.value) {
    if (!form.url.trim()) {
      fieldErrors.url = t('settingsNotifications.valFeishuUrlRequired')
      ok = false
    }
  } else if (isWecom.value) {
    if (!form.url.trim()) {
      fieldErrors.url = t('settingsNotifications.valWecomUrlRequired')
      ok = false
    }
  } else if (isDingtalk.value) {
    if (!form.url.trim()) {
      fieldErrors.url = t('settingsNotifications.valDingtalkUrlRequired')
      ok = false
    }
  } else if (isEmail.value) {
    if (!form.smtpHost.trim()) {
      fieldErrors.smtpHost = t('settingsNotifications.valSmtpHostRequired')
      ok = false
    }
    const port = Number(form.smtpPort)
    if (!form.smtpPort.trim() || Number.isNaN(port) || port <= 0) {
      fieldErrors.smtpPort = t('settingsNotifications.valPortInvalid')
      ok = false
    }
    if (!form.from.trim()) {
      fieldErrors.from = t('settingsNotifications.valFromRequired')
      ok = false
    }
    if (!form.to.trim()) {
      fieldErrors.to = t('settingsNotifications.valToRequired')
      ok = false
    }
  }
  return ok
}

function buildConfig(): ChannelConfigInput {
  if (isWebhook.value) {
    return { url: form.url.trim() }
  }
  if (isFeishu.value) {
    // 飞书:url=机器人 webhook 地址;password=可选签名密钥(机器人开启「签名校验」时填)。
    const cfg: ChannelConfigInput = { url: form.url.trim() }
    if (form.password) cfg.password = form.password
    return cfg
  }
  if (isWecom.value) {
    // 企业微信群机器人:仅 url(群机器人无需签名密钥)。
    return { url: form.url.trim() }
  }
  if (isDingtalk.value) {
    // 钉钉群机器人:url=机器人 webhook 地址;password=可选加签密钥(安全设置选「加签」时填)。
    const cfg: ChannelConfigInput = { url: form.url.trim() }
    if (form.password) cfg.password = form.password
    return cfg
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
      toast.success(t('settingsNotifications.toastChannelUpdated'))
    } else {
      await createChannel({
        name: form.name.trim(),
        type: form.type,
        enabled: form.enabled,
        config,
      })
      toast.success(t('settingsNotifications.toastChannelCreated'))
    }
    closeEditor()
    await loadChannels()
  } catch (err) {
    if (err instanceof HttpError && err.status === 422 && err.apiError) {
      mapValidationError(err.apiError.code, err.apiError.message)
    } else {
      toast.error(t('settingsNotifications.toastSaveFailed'), { detail: httpMessage(err, t('settingsNotifications.retryLater')) })
    }
  } finally {
    saving.value = false
  }
}

function mapValidationError(code: string, message: string): void {
  switch (code) {
    case 'invalid_channel':
      fieldErrors.name = t('settingsNotifications.errInvalidChannel')
      break
    case 'url_not_allowed':
      fieldErrors.url = t('settingsNotifications.errUrlNotAllowed')
      break
    case 'invalid_config':
      toast.error(t('settingsNotifications.toastSaveFailed'), { detail: t('settingsNotifications.errInvalidConfig') })
      break
    default:
      toast.error(t('settingsNotifications.toastSaveFailed'), { detail: message })
  }
}

// ─── delete ──────────────────────────────────────────────────────────────────

const deletingId = ref<string | null>(null)

async function handleDelete(ch: NotificationChannel): Promise<void> {
  if (!window.confirm(t('settingsNotifications.confirmDeleteChannel', { name: ch.name }))) return
  deletingId.value = ch.id
  try {
    await deleteChannel(ch.id)
    toast.success(t('settingsNotifications.toastChannelDeleted'))
    if (editingId.value === ch.id) closeEditor()
    await loadChannels()
  } catch (err) {
    toast.error(t('settingsNotifications.toastDeleteFailed'), { detail: httpMessage(err, t('settingsNotifications.retryLater')) })
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
      error: httpMessage(err, t('settingsNotifications.errTestFailed')),
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

const EVENTS = computed<EventMeta[]>(() => [
  { id: 'build_succeeded', label: t('settingsNotifications.evBuildSucceeded'), desc: t('settingsNotifications.evBuildSucceededDesc') },
  { id: 'build_failed', label: t('settingsNotifications.evBuildFailed'), desc: t('settingsNotifications.evBuildFailedDesc') },
  { id: 'deploy_succeeded', label: t('settingsNotifications.evDeploySucceeded'), desc: t('settingsNotifications.evDeploySucceededDesc') },
  { id: 'deploy_failed', label: t('settingsNotifications.evDeployFailed'), desc: t('settingsNotifications.evDeployFailedDesc') },
  { id: 'rollback', label: t('settingsNotifications.evRollback'), desc: t('settingsNotifications.evRollbackDesc') },
  { id: 'health_check_failed', label: t('settingsNotifications.evHealthCheckFailed'), desc: t('settingsNotifications.evHealthCheckFailedDesc') },
  { id: 'approval_required', label: t('settingsNotifications.evApprovalRequired'), desc: t('settingsNotifications.evApprovalRequiredDesc') },
  { id: 'anomaly_detected', label: t('settingsNotifications.evAnomalyDetected'), desc: t('settingsNotifications.evAnomalyDetectedDesc') },
])

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
    routesLoadError.value = httpMessage(err, t('settingsNotifications.errLoadRoutes'))
    routesLoadState.value = 'error'
  }
}

/** Routes grouped by event id (preserves server order within each group). */
const routesByEvent = computed<Record<string, NotificationRoute[]>>(() => {
  const map: Record<string, NotificationRoute[]> = {}
  for (const e of EVENTS.value) map[e.id] = []
  for (const r of routes.value) {
    if (!map[r.event]) map[r.event] = []
    map[r.event].push(r)
  }
  return map
})

/** Channel name lookup for rendering route rows. */
function channelName(channelId: string): string {
  return channels.value.find((c) => c.id === channelId)?.name ?? t('settingsNotifications.deletedChannel')
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
    toast.success(t('settingsNotifications.toastRouteAdded'))
    cancelAddRoute()
    await loadRoutes()
  } catch (err) {
    if (err instanceof HttpError && err.status === 422 && err.apiError) {
      toast.error(t('settingsNotifications.toastAddFailed'), { detail: err.apiError.message })
    } else {
      toast.error(t('settingsNotifications.toastAddFailed'), { detail: httpMessage(err, t('settingsNotifications.retryLater')) })
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
    toast.success(t('settingsNotifications.toastRouteRemoved'))
    await loadRoutes()
  } catch (err) {
    toast.error(t('settingsNotifications.toastRemoveFailed'), { detail: httpMessage(err, t('settingsNotifications.retryLater')) })
  } finally {
    deletingRouteId.value = null
  }
}

onMounted(loadRoutes)

// ─── per-pipeline routing override (Story 5.4 / FR-20) ─────────────────────────
// 「按项目覆盖」区:选一个项目 → 看它的事件路由。整体覆盖语义:项目一旦为某事件配了
// 任一项目级路由,该事件该项目就**只**走项目级(不与全局合并);未配 → 继承全局默认。
// 复用上方全局路由 UI 的事件 → 渠道形状,只多一个 projectId 维度。

const projects = ref<Project[]>([])
const projectsLoadState = ref<LoadState>('loading')
const projectsLoadError = ref('')

async function loadProjects(): Promise<void> {
  projectsLoadState.value = 'loading'
  projectsLoadError.value = ''
  try {
    projects.value = await listProjects()
    projectsLoadState.value = 'ready'
  } catch (err) {
    projectsLoadError.value = httpMessage(err, t('settingsNotifications.errLoadProjects'))
    projectsLoadState.value = 'error'
  }
}

/** Currently selected project for the override editor; '' = none selected. */
const selectedProjectId = ref('')
const projectRoutes = ref<NotificationRoute[]>([])
const projectRoutesLoadState = ref<LoadState>('ready')
const projectRoutesLoadError = ref('')

const selectedProject = computed<Project | null>(
  () => projects.value.find((p) => p.id === selectedProjectId.value) ?? null,
)

/** Events for which this project has at least one (override) route. */
const overriddenEventIds = computed<Set<string>>(() => {
  const s = new Set<string>()
  for (const r of projectRoutes.value) s.add(r.event)
  return s
})

/** Whole-scope override: a project with ANY override route stops inheriting global. */
const hasAnyOverride = computed(() => projectRoutes.value.length > 0)

async function loadProjectRoutes(): Promise<void> {
  if (!selectedProjectId.value) {
    projectRoutes.value = []
    projectRoutesLoadState.value = 'ready'
    return
  }
  projectRoutesLoadState.value = 'loading'
  projectRoutesLoadError.value = ''
  try {
    projectRoutes.value = await listRoutes(selectedProjectId.value)
    projectRoutesLoadState.value = 'ready'
  } catch (err) {
    projectRoutesLoadError.value = httpMessage(err, t('settingsNotifications.errLoadProjectRoutes'))
    projectRoutesLoadState.value = 'error'
  }
}

function onSelectProject(id: string): void {
  selectedProjectId.value = id
  cancelAddProjectRoute()
  void loadProjectRoutes()
}

/** Project routes grouped by event id (preserves server order within each group). */
const projectRoutesByEvent = computed<Record<string, NotificationRoute[]>>(() => {
  const map: Record<string, NotificationRoute[]> = {}
  for (const e of EVENTS.value) map[e.id] = []
  for (const r of projectRoutes.value) {
    if (!map[r.event]) map[r.event] = []
    map[r.event].push(r)
  }
  return map
})

// Add-project-route picker state, keyed per event.
const addingProjectEvent = ref<NotificationEvent | null>(null)
const addProjectChannelId = ref('')
const savingProjectRoute = ref(false)

function openAddProjectRoute(event: NotificationEvent): void {
  addingProjectEvent.value = event
  const used = new Set((projectRoutesByEvent.value[event] ?? []).map((r) => r.channelId))
  const free = channels.value.find((c) => !used.has(c.id))
  addProjectChannelId.value = free?.id ?? channels.value[0]?.id ?? ''
}

function cancelAddProjectRoute(): void {
  addingProjectEvent.value = null
  addProjectChannelId.value = ''
}

async function handleAddProjectRoute(): Promise<void> {
  if (!addingProjectEvent.value || !addProjectChannelId.value || !selectedProjectId.value) return
  savingProjectRoute.value = true
  try {
    await createRoute({
      projectId: selectedProjectId.value,
      event: addingProjectEvent.value,
      channelId: addProjectChannelId.value,
      enabled: true,
    })
    toast.success(t('settingsNotifications.toastProjectRouteAdded'))
    cancelAddProjectRoute()
    await loadProjectRoutes()
  } catch (err) {
    if (err instanceof HttpError && err.status === 422 && err.apiError) {
      toast.error(t('settingsNotifications.toastAddFailed'), { detail: err.apiError.message })
    } else {
      toast.error(t('settingsNotifications.toastAddFailed'), { detail: httpMessage(err, t('settingsNotifications.retryLater')) })
    }
  } finally {
    savingProjectRoute.value = false
  }
}

const deletingProjectRouteId = ref<string | null>(null)

async function handleDeleteProjectRoute(route: NotificationRoute): Promise<void> {
  deletingProjectRouteId.value = route.id
  try {
    await deleteRoute(route.id)
    toast.success(t('settingsNotifications.toastProjectRouteRemoved'))
    await loadProjectRoutes()
  } catch (err) {
    toast.error(t('settingsNotifications.toastRemoveFailed'), { detail: httpMessage(err, t('settingsNotifications.retryLater')) })
  } finally {
    deletingProjectRouteId.value = null
  }
}

onMounted(loadProjects)

// ─── notification templates (Story 5.3 / FR-21) ───────────────────────────────
// 「事件(可选按渠道)→ 标题/正文模板」自定义区:占位 {{name}} 纯文本替换(无 RCE);
// 未配模板的事件用平台默认文案(5-2 行为不变)。独立于上方渠道 / 路由既有区。

const templatesLoadState = ref<LoadState>('loading')
const templatesLoadError = ref('')
const templates = ref<NotificationTemplate[]>([])

async function loadTemplates(): Promise<void> {
  templatesLoadState.value = 'loading'
  templatesLoadError.value = ''
  try {
    templates.value = await listTemplates()
    templatesLoadState.value = 'ready'
  } catch (err) {
    templatesLoadError.value = httpMessage(err, t('settingsNotifications.errLoadTemplates'))
    templatesLoadState.value = 'error'
  }
}

/** Event label lookup for rendering template rows. */
function eventLabel(event: string): string {
  return EVENTS.value.find((e) => e.id === event)?.label ?? event
}

/** A template's channel scope label: empty channelId = 该事件通用. */
function templateScope(tpl: NotificationTemplate): string {
  if (!tpl.channelId) return t('settingsNotifications.scopeAllChannels')
  return channelName(tpl.channelId)
}

// Add/edit template form. A single inline form (create or edit) shared across rows.
interface TemplateForm {
  id: string | null
  event: NotificationEvent
  channelId: string
  titleTemplate: string
  bodyTemplate: string
}

const blankTemplateForm = (): TemplateForm => ({
  id: null,
  event: 'build_failed',
  channelId: '',
  titleTemplate: '',
  bodyTemplate: '',
})

const templateForm = reactive<TemplateForm>(blankTemplateForm())
const templateFormOpen = ref(false)
const savingTemplate = ref(false)

const availableVariables = TEMPLATE_VARIABLES

// Literal {{x}} strings rendered in the template (kept out of the template to avoid
// Vue's interpolation parser choking on nested {{ }}).
const placeholderSyntax = computed(() => `{{${t('settingsNotifications.placeholderVar')}}}`)
function placeholderToken(name: string): string {
  return `{{${name}}}`
}

function openCreateTemplate(): void {
  Object.assign(templateForm, blankTemplateForm())
  templateFormOpen.value = true
}

function openEditTemplate(tpl: NotificationTemplate): void {
  Object.assign(templateForm, {
    id: tpl.id,
    event: tpl.event,
    channelId: tpl.channelId ?? '',
    titleTemplate: tpl.titleTemplate,
    bodyTemplate: tpl.bodyTemplate,
  })
  templateFormOpen.value = true
}

function cancelTemplateForm(): void {
  templateFormOpen.value = false
  Object.assign(templateForm, blankTemplateForm())
}

async function handleSaveTemplate(): Promise<void> {
  savingTemplate.value = true
  try {
    const payload = {
      event: templateForm.event,
      channelId: templateForm.channelId || undefined,
      titleTemplate: templateForm.titleTemplate,
      bodyTemplate: templateForm.bodyTemplate,
    }
    if (templateForm.id) {
      await updateTemplate(templateForm.id, payload)
      toast.success(t('settingsNotifications.toastTemplateUpdated'))
    } else {
      await createTemplate(payload)
      toast.success(t('settingsNotifications.toastTemplateAdded'))
    }
    cancelTemplateForm()
    await loadTemplates()
  } catch (err) {
    if (err instanceof HttpError && err.status === 422 && err.apiError) {
      toast.error(t('settingsNotifications.toastSaveFailed'), { detail: err.apiError.message })
    } else {
      toast.error(t('settingsNotifications.toastSaveFailed'), { detail: httpMessage(err, t('settingsNotifications.retryLater')) })
    }
  } finally {
    savingTemplate.value = false
  }
}

const deletingTemplateId = ref<string | null>(null)

async function handleDeleteTemplate(tpl: NotificationTemplate): Promise<void> {
  deletingTemplateId.value = tpl.id
  try {
    await deleteTemplate(tpl.id)
    toast.success(t('settingsNotifications.toastTemplateRemoved'))
    await loadTemplates()
  } catch (err) {
    toast.error(t('settingsNotifications.toastRemoveFailed'), { detail: httpMessage(err, t('settingsNotifications.retryLater')) })
  } finally {
    deletingTemplateId.value = null
  }
}

onMounted(loadTemplates)

// ─── helpers ─────────────────────────────────────────────────────────────────

function httpMessage(err: unknown, fallback: string): string {
  if (err instanceof HttpError) {
    if (err.status === 0) return t('settingsNotifications.errNoConnection')
    return err.apiError?.message ?? t('settingsNotifications.errRequestFailed', { status: err.status })
  }
  return err instanceof Error ? err.message : fallback
}

function configSummary(ch: NotificationChannel): string {
  if (ch.type === 'webhook' || ch.type === 'feishu' || ch.type === 'wecom' || ch.type === 'dingtalk') {
    return ch.config.url ?? ''
  }
  if (ch.type === 'email') {
    const port = ch.config.smtpPort ? `:${ch.config.smtpPort}` : ''
    return `${ch.config.smtpHost ?? ''}${port} → ${ch.config.to ?? ''}`
  }
  return t('settingsNotifications.comingSoon')
}
</script>

<template>
  <div class="nt-root">
    <!-- ─── section header ──────────────────────────────────────────────────── -->
    <div class="section-head">
      <div class="section-head-text">
        <h2 class="section-title">{{ t('settingsNotifications.channelsTitle') }}</h2>
        <p class="section-desc">
          {{ t('settingsNotifications.channelsDesc') }}
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
        {{ t('settingsNotifications.addChannel') }}
      </AppButton>
    </div>

    <!-- ─── 通知语言(外发文案语言,与界面语言解耦)─────────────────────────── -->
    <div class="nt-lang">
      <div class="nt-lang-text">
        <span class="nt-lang-title">{{ t('settingsNotifications.langTitle') }}</span>
        <span class="nt-lang-desc">{{ t('settingsNotifications.langDesc') }}</span>
      </div>
      <select
        v-model="notifyLang"
        class="nt-lang-select"
        :aria-label="t('settingsNotifications.langTitle')"
        @change="onNotifyLangChange"
      >
        <option v-for="l in localeOptions" :key="l.code" :value="l.code">{{ l.label }}</option>
      </select>
    </div>

    <!-- ─── load error ──────────────────────────────────────────────────────── -->
    <div v-if="loadState === 'error'" class="banner banner--error" role="alert">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
      </svg>
      <span>{{ loadError }}</span>
      <button class="banner-retry" @click="loadChannels">↻ {{ t('settingsNotifications.retry') }}</button>
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
            {{ editorMode === 'edit' ? t('settingsNotifications.editChannel') : t('settingsNotifications.addChannel') }}
            <button class="editor-close" type="button" :aria-label="t('settingsNotifications.close')" @click="closeEditor">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
            </button>
          </div>
          <div class="panel-body">
            <form class="config-form" novalidate @submit.prevent="handleSave">

              <!-- Type selector (locked when editing — type is immutable) -->
              <div class="config-row">
                <span class="field-label">{{ t('settingsNotifications.channelType') }}</span>
                <div class="type-grid" role="radiogroup" :aria-label="t('settingsNotifications.channelType')">
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
                    <span v-if="!m.implemented" class="type-soon">{{ t('settingsNotifications.notImplemented') }}</span>
                  </button>
                </div>
              </div>

              <!-- Name -->
              <div class="config-row">
                <FormField :label="t('settingsNotifications.channelName')" field-id="nt-name" :error="fieldErrors.name" required>
                  <template #default="{ fieldId, ariaDescribedby }">
                    <input
                      :id="fieldId"
                      v-model="form.name"
                      type="text"
                      class="field-input"
                      :class="{ 'field-input--error': fieldErrors.name }"
                      :placeholder="t('settingsNotifications.channelNamePlaceholder')"
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
                  :label="t('settingsNotifications.webhookUrl')"
                  field-id="nt-url"
                  :error="fieldErrors.url"
                  :hint="t('settingsNotifications.webhookUrlHint')"
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

              <!-- Feishu fields -->
              <template v-else-if="isFeishu">
                <div class="config-row">
                  <FormField
                    :label="t('settingsNotifications.feishuUrl')"
                    field-id="nt-fs-url"
                    :error="fieldErrors.url"
                    :hint="t('settingsNotifications.feishuUrlHint')"
                    required
                  >
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.url"
                        type="url"
                        class="field-input field-input--mono"
                        :class="{ 'field-input--error': fieldErrors.url }"
                        placeholder="https://open.feishu.cn/open-apis/bot/v2/hook/…"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                        @input="fieldErrors.url = ''"
                      />
                    </template>
                  </FormField>
                </div>
                <div class="config-row">
                  <FormField
                    :label="t('settingsNotifications.signSecret')"
                    field-id="nt-fs-secret"
                    :hint="hasStoredPassword ? t('settingsNotifications.secretStoredHint') : t('settingsNotifications.signSecretHint')"
                  >
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.password"
                        type="password"
                        class="field-input field-input--mono"
                        :placeholder="hasStoredPassword ? t('settingsNotifications.secretStoredPlaceholder') : t('settingsNotifications.signSecretPlaceholder')"
                        autocomplete="new-password"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                      />
                    </template>
                  </FormField>
                </div>
              </template>

              <!-- Wecom (企业微信群机器人) fields -->
              <div v-else-if="isWecom" class="config-row">
                <FormField
                  :label="t('settingsNotifications.wecomUrl')"
                  field-id="nt-wc-url"
                  :error="fieldErrors.url"
                  :hint="t('settingsNotifications.wecomUrlHint')"
                  required
                >
                  <template #default="{ fieldId, ariaDescribedby }">
                    <input
                      :id="fieldId"
                      v-model="form.url"
                      type="url"
                      class="field-input field-input--mono"
                      :class="{ 'field-input--error': fieldErrors.url }"
                      placeholder="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=…"
                      :aria-describedby="ariaDescribedby"
                      :disabled="saving"
                      @input="fieldErrors.url = ''"
                    />
                  </template>
                </FormField>
              </div>

              <!-- Dingtalk (钉钉群机器人) fields -->
              <template v-else-if="isDingtalk">
                <div class="config-row">
                  <FormField
                    :label="t('settingsNotifications.dingtalkUrl')"
                    field-id="nt-dt-url"
                    :error="fieldErrors.url"
                    :hint="t('settingsNotifications.dingtalkUrlHint')"
                    required
                  >
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.url"
                        type="url"
                        class="field-input field-input--mono"
                        :class="{ 'field-input--error': fieldErrors.url }"
                        placeholder="https://oapi.dingtalk.com/robot/send?access_token=…"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                        @input="fieldErrors.url = ''"
                      />
                    </template>
                  </FormField>
                </div>
                <div class="config-row">
                  <FormField
                    :label="t('settingsNotifications.dingtalkSignSecret')"
                    field-id="nt-dt-secret"
                    :hint="hasStoredPassword ? t('settingsNotifications.secretStoredHint') : t('settingsNotifications.dingtalkSignSecretHint')"
                  >
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.password"
                        type="password"
                        class="field-input field-input--mono"
                        :placeholder="hasStoredPassword ? t('settingsNotifications.secretStoredPlaceholder') : t('settingsNotifications.dingtalkSignSecretPlaceholder')"
                        autocomplete="new-password"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                      />
                    </template>
                  </FormField>
                </div>
              </template>

              <!-- Email fields -->
              <template v-else-if="isEmail">
                <div class="config-grid">
                  <FormField :label="t('settingsNotifications.smtpHost')" field-id="nt-host" :error="fieldErrors.smtpHost" required>
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
                  <FormField :label="t('settingsNotifications.port')" field-id="nt-port" :error="fieldErrors.smtpPort" required>
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
                  <FormField :label="t('settingsNotifications.fromAddr')" field-id="nt-from" :error="fieldErrors.from" required>
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
                  <FormField :label="t('settingsNotifications.toAddr')" field-id="nt-to" :error="fieldErrors.to" :hint="t('settingsNotifications.toAddrHint')" required>
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
                  <FormField :label="t('settingsNotifications.username')" field-id="nt-user" :hint="t('settingsNotifications.usernameHint')">
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.username"
                        type="text"
                        class="field-input field-input--mono"
                        :placeholder="t('settingsNotifications.usernamePlaceholder')"
                        autocomplete="off"
                        :aria-describedby="ariaDescribedby"
                        :disabled="saving"
                      />
                    </template>
                  </FormField>
                  <FormField
                    :label="t('settingsNotifications.smtpPassword')"
                    field-id="nt-pw"
                    :hint="hasStoredPassword ? t('settingsNotifications.secretStoredHint') : t('settingsNotifications.smtpPasswordHint')"
                  >
                    <template #default="{ fieldId, ariaDescribedby }">
                      <input
                        :id="fieldId"
                        v-model="form.password"
                        type="password"
                        class="field-input field-input--mono"
                        :placeholder="hasStoredPassword ? t('settingsNotifications.secretStoredPlaceholder') : t('settingsNotifications.smtpPasswordPlaceholder')"
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
                {{ t('settingsNotifications.placeholderTypeHint') }}
              </div>

              <!-- Enabled toggle -->
              <div class="toggle-row">
                <button
                  type="button"
                  class="toggle-track"
                  :class="{ 'toggle-track--on': form.enabled }"
                  role="switch"
                  :aria-checked="form.enabled"
                  :aria-label="t('settingsNotifications.enableChannelAria')"
                  :disabled="saving"
                  @click="form.enabled = !form.enabled"
                >
                  <span class="toggle-thumb" />
                </button>
                <div class="toggle-label">
                  <strong>{{ t('settingsNotifications.enableChannel') }}</strong>
                  <span>{{ t('settingsNotifications.enableChannelDesc') }}</span>
                </div>
              </div>

              <!-- Save bar -->
              <div class="editor-actions">
                <AppButton variant="ghost" type="button" :disabled="saving" @click="closeEditor">{{ t('settingsNotifications.cancel') }}</AppButton>
                <AppButton variant="primary" type="submit" :loading="saving">
                  {{ editorMode === 'edit' ? t('settingsNotifications.saveChanges') : t('settingsNotifications.createChannel') }}
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
        <strong>{{ t('settingsNotifications.emptyTitle') }}</strong>
        <p>{{ t('settingsNotifications.emptyDesc') }}</p>
        <AppButton variant="primary" type="button" @click="openCreate">{{ t('settingsNotifications.addChannel') }}</AppButton>
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
                  {{ ch.enabled ? t('settingsNotifications.statusEnabled') : t('settingsNotifications.statusDisabled') }}
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
                    {{ t('settingsNotifications.testOk', { ms: testResults[ch.id].latencyMs }) }}
                    <span v-if="testResults[ch.id].detail" class="test-detail">· {{ testResults[ch.id].detail }}</span>
                  </template>
                  <template v-else>
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                      <circle cx="12" cy="12" r="9" /><path d="M15 9l-6 6M9 9l6 6" />
                    </svg>
                    {{ testResults[ch.id].error ?? t('settingsNotifications.testFail') }}
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
              {{ t('settingsNotifications.testSend') }}
            </AppButton>
            <button class="icon-btn" type="button" :aria-label="t('settingsNotifications.edit')" @click="openEdit(ch)">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
              </svg>
            </button>
            <button
              class="icon-btn icon-btn--danger"
              type="button"
              :aria-label="t('settingsNotifications.delete')"
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
          <h2 id="routes-heading" class="section-title">{{ t('settingsNotifications.routesTitle') }}</h2>
          <p class="section-desc">
            {{ t('settingsNotifications.routesDescPre') }}<strong>{{ t('settingsNotifications.routesDescStrong') }}</strong>{{ t('settingsNotifications.routesDescPost') }}
          </p>
        </div>
      </div>

      <!-- routes load error -->
      <div v-if="routesLoadState === 'error'" class="banner banner--error" role="alert">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
        </svg>
        <span>{{ routesLoadError }}</span>
        <button class="banner-retry" @click="loadRoutes">↻ {{ t('settingsNotifications.retry') }}</button>
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
        {{ t('settingsNotifications.routesNoChannels') }}
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
              {{ t('settingsNotifications.addChannelShort') }}
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
                :aria-label="t('settingsNotifications.removeChannelAria', { name: channelName(r.channelId) })"
                :disabled="deletingRouteId === r.id"
                @click="handleDeleteRoute(r)"
              >
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                  <path d="M18 6 6 18M6 6l12 12" />
                </svg>
              </button>
            </span>
          </div>
          <div v-else-if="addingEvent !== ev.id" class="route-empty">{{ t('settingsNotifications.routeEmpty') }}</div>

          <!-- add-route inline picker -->
          <div v-if="addingEvent === ev.id" class="route-add">
            <select v-model="addChannelId" class="route-select" :disabled="savingRoute" :aria-label="t('settingsNotifications.selectChannel')">
              <option v-for="c in channels" :key="c.id" :value="c.id">
                {{ c.name }}{{ c.enabled ? '' : t('settingsNotifications.disabledSuffix') }}
              </option>
            </select>
            <AppButton variant="primary" type="button" :loading="savingRoute" :disabled="!addChannelId" @click="handleAddRoute">
              {{ t('settingsNotifications.add') }}
            </AppButton>
            <AppButton variant="ghost" type="button" :disabled="savingRoute" @click="cancelAddRoute">{{ t('settingsNotifications.cancel') }}</AppButton>
          </div>
        </li>
      </ul>
    </section>

    <!-- ─── per-pipeline routing override (Story 5.4 / FR-20) ─────────────────── -->
    <section class="override-section" aria-labelledby="override-heading">
      <div class="section-head">
        <div class="section-head-text">
          <h2 id="override-heading" class="section-title">{{ t('settingsNotifications.overrideTitle') }}</h2>
          <p class="section-desc">
            {{ t('settingsNotifications.overrideDescA') }}<strong>{{ t('settingsNotifications.overrideDescStrong1') }}</strong>{{ t('settingsNotifications.overrideDescB') }}<strong>{{ t('settingsNotifications.overrideDescStrong2') }}</strong>{{ t('settingsNotifications.overrideDescC') }}<strong>{{ t('settingsNotifications.overrideDescStrong3') }}</strong>{{ t('settingsNotifications.overrideDescD') }}
          </p>
        </div>
      </div>

      <!-- projects load error -->
      <div v-if="projectsLoadState === 'error'" class="banner banner--error" role="alert">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
        </svg>
        <span>{{ projectsLoadError }}</span>
        <button class="banner-retry" @click="loadProjects">↻ {{ t('settingsNotifications.retry') }}</button>
      </div>

      <!-- projects loading -->
      <div v-else-if="projectsLoadState === 'loading'" class="panel panel--anim">
        <div class="skel-body">
          <div v-for="i in 1" :key="i" class="skel skel--row" aria-hidden="true" />
        </div>
      </div>

      <!-- no channels -->
      <div v-else-if="channels.length === 0" class="soon-hint" role="note">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
        </svg>
        {{ t('settingsNotifications.overrideNoChannels') }}
      </div>

      <!-- no projects -->
      <div v-else-if="projects.length === 0" class="soon-hint" role="note">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
        </svg>
        {{ t('settingsNotifications.overrideNoProjects') }}
      </div>

      <template v-else>
        <!-- project picker -->
        <div class="override-picker">
          <FormField :label="t('settingsNotifications.selectProject')" field-id="override-project" :hint="t('settingsNotifications.selectProjectHint')">
            <template #default="{ fieldId, ariaDescribedby }">
              <select
                :id="fieldId"
                class="route-select"
                :value="selectedProjectId"
                :aria-describedby="ariaDescribedby"
                :aria-label="t('settingsNotifications.selectProjectAria')"
                @change="onSelectProject(($event.target as HTMLSelectElement).value)"
              >
                <option value="">{{ t('settingsNotifications.selectProjectPlaceholder') }}</option>
                <option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option>
              </select>
            </template>
          </FormField>
        </div>

        <!-- selected project routes -->
        <template v-if="selectedProjectId">
          <!-- inheritance banner -->
          <div v-if="!hasAnyOverride && projectRoutesLoadState === 'ready'" class="inherit-banner" role="note">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M12 2v6m0 0 3-3m-3 3L9 5M5 12H2m20 0h-3M12 22v-6m0 0 3 3m-3-3-3 3" />
            </svg>
            <span>
              <strong>{{ selectedProject?.name }}</strong>{{ t('settingsNotifications.inheritBannerA') }}<strong>{{ t('settingsNotifications.inheritBannerStrong1') }}</strong>{{ t('settingsNotifications.inheritBannerB') }}<strong>{{ t('settingsNotifications.inheritBannerStrong2') }}</strong>{{ t('settingsNotifications.inheritBannerC') }}
            </span>
          </div>
          <div v-else-if="hasAnyOverride" class="custom-banner" role="note">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M12 2 2 7l10 5 10-5-10-5ZM2 17l10 5 10-5M2 12l10 5 10-5" />
            </svg>
            <span>
              <strong>{{ selectedProject?.name }}</strong>{{ t('settingsNotifications.customBannerA') }}<strong>{{ t('settingsNotifications.customBannerStrong') }}</strong>{{ t('settingsNotifications.customBannerB') }}
            </span>
          </div>

          <!-- project routes load error -->
          <div v-if="projectRoutesLoadState === 'error'" class="banner banner--error" role="alert">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
            </svg>
            <span>{{ projectRoutesLoadError }}</span>
            <button class="banner-retry" @click="loadProjectRoutes">↻ {{ t('settingsNotifications.retry') }}</button>
          </div>

          <!-- project routes loading -->
          <div v-else-if="projectRoutesLoadState === 'loading'" class="panel panel--anim">
            <div class="skel-body">
              <div v-for="i in 3" :key="i" class="skel skel--row" aria-hidden="true" />
            </div>
          </div>

          <!-- event → project channel mapping table -->
          <ul v-else class="event-list">
            <li v-for="ev in EVENTS" :key="ev.id" class="event-card panel--anim">
              <div class="event-head">
                <div class="event-text">
                  <span class="event-name">{{ ev.label }}</span>
                  <span class="event-code mono">{{ ev.id }}</span>
                  <span
                    v-if="overriddenEventIds.has(ev.id)"
                    class="scope-tag scope-tag--project"
                  >{{ t('settingsNotifications.scopeProject') }}</span>
                  <span v-else class="scope-tag scope-tag--inherit">{{ t('settingsNotifications.scopeInherit') }}</span>
                </div>
                <AppButton
                  v-if="addingProjectEvent !== ev.id"
                  variant="default"
                  type="button"
                  @click="openAddProjectRoute(ev.id)"
                >
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                    <path d="M12 5v14M5 12h14" />
                  </svg>
                  {{ t('settingsNotifications.addChannelShort') }}
                </AppButton>
              </div>

              <!-- project routed channel chips -->
              <div
                v-if="projectRoutesByEvent[ev.id] && projectRoutesByEvent[ev.id].length > 0"
                class="route-chips"
              >
                <span v-for="r in projectRoutesByEvent[ev.id]" :key="r.id" class="route-chip">
                  <span class="route-chip-dot" :class="{ 'route-chip-dot--on': r.enabled }" aria-hidden="true" />
                  {{ channelName(r.channelId) }}
                  <button
                    class="route-chip-x"
                    type="button"
                    :aria-label="t('settingsNotifications.removeChannelAria', { name: channelName(r.channelId) })"
                    :disabled="deletingProjectRouteId === r.id"
                    @click="handleDeleteProjectRoute(r)"
                  >
                    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                      <path d="M18 6 6 18M6 6l12 12" />
                    </svg>
                  </button>
                </span>
              </div>
              <div v-else-if="addingProjectEvent !== ev.id" class="route-empty">
                {{ t('settingsNotifications.projectRouteEmpty') }}
              </div>

              <!-- add-project-route inline picker -->
              <div v-if="addingProjectEvent === ev.id" class="route-add">
                <select v-model="addProjectChannelId" class="route-select" :disabled="savingProjectRoute" :aria-label="t('settingsNotifications.selectChannel')">
                  <option v-for="c in channels" :key="c.id" :value="c.id">
                    {{ c.name }}{{ c.enabled ? '' : t('settingsNotifications.disabledSuffix') }}
                  </option>
                </select>
                <AppButton variant="primary" type="button" :loading="savingProjectRoute" :disabled="!addProjectChannelId" @click="handleAddProjectRoute">
                  {{ t('settingsNotifications.add') }}
                </AppButton>
                <AppButton variant="ghost" type="button" :disabled="savingProjectRoute" @click="cancelAddProjectRoute">{{ t('settingsNotifications.cancel') }}</AppButton>
              </div>
            </li>
          </ul>
        </template>
      </template>
    </section>

    <!-- ─── notification templates (Story 5.3 / FR-21) ───────────────────────── -->
    <section class="templates-section" aria-labelledby="templates-heading">
      <div class="section-head">
        <div class="section-head-text">
          <h2 id="templates-heading" class="section-title">{{ t('settingsNotifications.templatesTitle') }}</h2>
          <p class="section-desc">
            {{ t('settingsNotifications.templatesDescA') }}<strong>{{ t('settingsNotifications.templatesDescStrong1') }}</strong>{{ t('settingsNotifications.templatesDescB') }}<code class="mono">{{ placeholderSyntax }}</code>{{ t('settingsNotifications.templatesDescC') }}<strong>{{ t('settingsNotifications.templatesDescStrong2') }}</strong>{{ t('settingsNotifications.templatesDescD') }}
          </p>
        </div>
        <AppButton
          v-if="templatesLoadState === 'ready' && !templateFormOpen"
          variant="default"
          type="button"
          @click="openCreateTemplate"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
          {{ t('settingsNotifications.addTemplate') }}
        </AppButton>
      </div>

      <!-- available variables hint -->
      <div class="tpl-vars" role="note">
        <span class="tpl-vars-label">{{ t('settingsNotifications.availableVars') }}</span>
        <code v-for="v in availableVariables" :key="v" class="tpl-var mono">{{ placeholderToken(v) }}</code>
      </div>

      <!-- templates load error -->
      <div v-if="templatesLoadState === 'error'" class="banner banner--error" role="alert">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
        </svg>
        <span>{{ templatesLoadError }}</span>
        <button class="banner-retry" @click="loadTemplates">↻ {{ t('settingsNotifications.retry') }}</button>
      </div>

      <!-- templates loading -->
      <div v-else-if="templatesLoadState === 'loading'" class="panel panel--anim">
        <div class="skel-body">
          <div v-for="i in 2" :key="i" class="skel skel--row" aria-hidden="true" />
        </div>
      </div>

      <template v-else>
        <!-- add/edit template form -->
        <form v-if="templateFormOpen" class="tpl-form panel--anim" @submit.prevent="handleSaveTemplate">
          <div class="tpl-form-row">
            <FormField :label="t('settingsNotifications.tplEvent')" field-id="tpl-event" required>
              <template #default="{ fieldId, ariaDescribedby }">
                <select :id="fieldId" v-model="templateForm.event" class="route-select" :aria-describedby="ariaDescribedby" :disabled="savingTemplate">
                  <option v-for="ev in EVENTS" :key="ev.id" :value="ev.id">{{ ev.label }}({{ ev.id }})</option>
                </select>
              </template>
            </FormField>
            <FormField :label="t('settingsNotifications.tplChannel')" field-id="tpl-channel" :hint="t('settingsNotifications.tplChannelHint')">
              <template #default="{ fieldId, ariaDescribedby }">
                <select :id="fieldId" v-model="templateForm.channelId" class="route-select" :aria-describedby="ariaDescribedby" :disabled="savingTemplate">
                  <option value="">{{ t('settingsNotifications.tplChannelAll') }}</option>
                  <option v-for="c in channels" :key="c.id" :value="c.id">{{ c.name }}{{ c.enabled ? '' : t('settingsNotifications.disabledSuffix') }}</option>
                </select>
              </template>
            </FormField>
          </div>

          <FormField :label="t('settingsNotifications.tplTitle')" field-id="tpl-title" :hint="t('settingsNotifications.tplTitleHint')">
            <template #default="{ fieldId, ariaDescribedby }">
              <input
                :id="fieldId"
                v-model="templateForm.titleTemplate"
                class="field-input field-input--mono"
                type="text"
                :aria-describedby="ariaDescribedby"
                :disabled="savingTemplate"
                :placeholder="t('settingsNotifications.tplTitlePlaceholder')"
              />
            </template>
          </FormField>

          <FormField :label="t('settingsNotifications.tplBody')" field-id="tpl-body">
            <template #default="{ fieldId, ariaDescribedby }">
              <textarea
                :id="fieldId"
                v-model="templateForm.bodyTemplate"
                class="field-input field-input--mono tpl-textarea"
                rows="4"
                :aria-describedby="ariaDescribedby"
                :disabled="savingTemplate"
                :placeholder="t('settingsNotifications.tplBodyPlaceholder')"
              />
            </template>
          </FormField>

          <div class="tpl-form-actions">
            <AppButton variant="primary" type="submit" :loading="savingTemplate">
              {{ templateForm.id ? t('settingsNotifications.saveEdit') : t('settingsNotifications.createTemplate') }}
            </AppButton>
            <AppButton variant="ghost" type="button" :disabled="savingTemplate" @click="cancelTemplateForm">{{ t('settingsNotifications.cancel') }}</AppButton>
          </div>
        </form>

        <!-- empty state -->
        <div v-if="templates.length === 0 && !templateFormOpen" class="soon-hint" role="note">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
          </svg>
          {{ t('settingsNotifications.tplEmpty') }}
        </div>

        <!-- template list -->
        <ul v-if="templates.length > 0" class="tpl-list">
          <li v-for="tpl in templates" :key="tpl.id" class="tpl-card panel--anim">
            <div class="tpl-card-head">
              <div class="tpl-card-meta">
                <span class="event-name">{{ eventLabel(tpl.event) }}</span>
                <span class="event-code mono">{{ tpl.event }}</span>
                <span class="tpl-scope">{{ templateScope(tpl) }}</span>
              </div>
              <div class="tpl-card-actions">
                <button class="tpl-icon-btn" type="button" :aria-label="t('settingsNotifications.editTemplateAria', { event: eventLabel(tpl.event) })" @click="openEditTemplate(tpl)">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                    <path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
                  </svg>
                </button>
                <button
                  class="tpl-icon-btn tpl-icon-btn--danger"
                  type="button"
                  :aria-label="t('settingsNotifications.deleteTemplateAria', { event: eventLabel(tpl.event) })"
                  :disabled="deletingTemplateId === tpl.id"
                  @click="handleDeleteTemplate(tpl)"
                >
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                    <path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6" />
                  </svg>
                </button>
              </div>
            </div>
            <div class="tpl-card-body">
              <div v-if="tpl.titleTemplate" class="tpl-line">
                <span class="tpl-line-label">{{ t('settingsNotifications.tplLineTitle') }}</span>
                <code class="mono">{{ tpl.titleTemplate }}</code>
              </div>
              <div v-if="tpl.bodyTemplate" class="tpl-line">
                <span class="tpl-line-label">{{ t('settingsNotifications.tplLineBody') }}</span>
                <code class="mono tpl-body-code">{{ tpl.bodyTemplate }}</code>
              </div>
            </div>
          </li>
        </ul>
      </template>
    </section>
  </div>
</template>

<style scoped>
.nt-root {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
}

/* ─── notification language ───────────────────────────────────────────────── */
.nt-lang {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
}
.nt-lang-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.nt-lang-title {
  font-weight: 600;
  color: var(--color-text);
}
.nt-lang-desc {
  font-size: var(--text-caption);
  color: var(--color-dim);
}
.nt-lang-select {
  flex-shrink: 0;
  padding: 7px 11px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  background: var(--color-bg);
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: var(--text-body);
  cursor: pointer;
}
.nt-lang-select:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
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

/* ─── notification templates (Story 5.3) ──────────────────────────────────── */
.templates-section {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.tpl-vars {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  background: var(--color-inset);
  border: 1px dashed var(--color-border);
  border-radius: var(--rounded);
}

.tpl-vars-label {
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--color-text-soft);
}

.tpl-var {
  padding: 2px 7px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  border-radius: 5px;
  font-size: 0.78rem;
}

.tpl-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 18px;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg);
}

.tpl-form-row {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
}

.tpl-form-row > * {
  flex: 1 1 220px;
}

.tpl-textarea {
  min-height: 92px;
  resize: vertical;
  line-height: 1.5;
}

.tpl-form-actions {
  display: flex;
  gap: 10px;
}

.tpl-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.tpl-card {
  padding: 16px 18px;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg);
}

.tpl-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.tpl-card-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.tpl-scope {
  font-size: 0.78rem;
  color: var(--color-text-soft);
  padding: 2px 8px;
  background: var(--color-inset);
  border-radius: 999px;
}

.tpl-card-actions {
  display: flex;
  gap: 6px;
}

.tpl-icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border-radius: var(--rounded);
  border: 1px solid var(--color-border);
  background: var(--color-surface);
  color: var(--color-text-soft);
  cursor: pointer;
  transition: color var(--duration-fast, 150ms), border-color var(--duration-fast, 150ms), background var(--duration-fast, 150ms);
}

.tpl-icon-btn:hover {
  color: var(--color-text);
  border-color: var(--color-border-strong, var(--color-border));
  background: var(--color-inset);
}

.tpl-icon-btn--danger:hover {
  color: var(--color-danger, oklch(58% 0.2 25));
  border-color: var(--color-danger, oklch(58% 0.2 25));
}

.tpl-icon-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.tpl-card-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.tpl-line {
  display: flex;
  gap: 10px;
  align-items: baseline;
}

.tpl-line-label {
  flex-shrink: 0;
  width: 38px;
  font-size: 0.74rem;
  font-weight: 600;
  color: var(--color-text-soft);
}

.tpl-line code {
  font-size: 0.82rem;
  color: var(--color-text);
  word-break: break-word;
}

.tpl-body-code {
  white-space: pre-wrap;
}

/* ─── per-pipeline override (Story 5.4) ───────────────────────────────────── */
.override-picker {
  max-width: 420px;
}

.inherit-banner,
.custom-banner {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 11px 14px;
  border-radius: var(--rounded);
  font-size: 0.82rem;
  line-height: 1.55;
  color: var(--color-dim);
}

.inherit-banner {
  background: var(--color-inset);
  border: 1px solid var(--color-border);
}

.inherit-banner svg { flex-shrink: 0; color: var(--color-faint); margin-top: 1px; }

.custom-banner {
  background: var(--color-primary-soft);
  border: 1px solid var(--color-primary);
  color: var(--color-text);
}

.custom-banner svg { flex-shrink: 0; color: var(--color-primary); margin-top: 1px; }

.scope-tag {
  font-size: 0.64rem;
  font-weight: 600;
  padding: 2px 7px;
  border-radius: var(--rounded-full);
  letter-spacing: 0.01em;
}

.scope-tag--project {
  background: var(--color-primary-soft);
  color: var(--color-primary);
}

.scope-tag--inherit {
  background: var(--color-inset);
  color: var(--color-faint);
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
