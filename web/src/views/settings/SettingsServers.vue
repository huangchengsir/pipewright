<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import {
  listServers,
  createServer,
  updateServer,
  deleteServer,
  testServer,
} from '../../api/servers'
import type {
  Server,
  CreateServerInput,
  UpdateServerInput,
  ServerTestResult,
} from '../../api/servers'
import { listCredentials } from '../../api/credentials'
import type { Credential } from '../../api/credentials'
import { HttpError } from '../../api/http'
import ServiceLogViewer from '../../components/ops/ServiceLogViewer.vue'

// ─── state ──────────────────────────────────────────────────────────────────

type LoadState = 'idle' | 'loading' | 'error'

const loadState = ref<LoadState>('idle')
const loadError = ref('')
const servers = ref<Server[]>([])

// SSH credentials available to bind (type ssh_key only).
const sshCredentials = ref<Credential[]>([])

// ─── add / edit modal ───────────────────────────────────────────────────────

const modalOpen = ref(false)
const modalMode = ref<'add' | 'edit'>('add')
const editingId = ref<string | null>(null)

const form = ref({
  name: '',
  host: '',
  port: 22,
  user: '',
  credentialId: '',
})

const formErrors = ref({
  name: '',
  host: '',
  port: '',
  user: '',
  credentialId: '',
})

const formBanner = ref('')
const formSubmitting = ref(false)

// ─── delete confirm ─────────────────────────────────────────────────────────

const deleteModalOpen = ref(false)
const deletingServer = ref<Server | null>(null)
const deleteSubmitting = ref(false)
const deleteBanner = ref('')

// ─── test connection ────────────────────────────────────────────────────────

const testingId = ref<string | null>(null)
const testResults = ref<Record<string, ServerTestResult>>({})

// ─── service logs viewer (Story 6-2, FR-16) ──────────────────────────────────

const logsModalOpen = ref(false)
const logsServer = ref<Server | null>(null)

function openLogsModal(s: Server): void {
  logsServer.value = s
  logsModalOpen.value = true
}

function closeLogsModal(): void {
  logsModalOpen.value = false
  logsServer.value = null
}

// ─── helpers ────────────────────────────────────────────────────────────────

const hasSSHCredentials = computed(() => sshCredentials.value.length > 0)

function credentialLabel(id: string): string {
  const c = sshCredentials.value.find((c) => c.id === id)
  return c ? c.name : '(凭据已删除)'
}

// ─── data loading ────────────────────────────────────────────────────────────

async function loadServers(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    const [srv, creds] = await Promise.all([listServers(), listCredentials()])
    servers.value = srv
    sshCredentials.value = creds.filter((c) => c.type === 'ssh_key')
    loadState.value = 'idle'
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        loadError.value = '无法连接到服务器,请检查后端是否运行后重试'
      } else if (err.apiError?.code === 'vault_unconfigured') {
        loadError.value = '保险库未配置 master key,请联系管理员设置 PIPEWRIGHT_MASTER_KEY 环境变量'
      } else {
        loadError.value = err.apiError?.message ?? `加载服务器失败(${err.status})`
      }
    } else {
      loadError.value = '加载服务器失败,请稍后重试'
    }
    loadState.value = 'error'
  }
}

onMounted(loadServers)

// ─── modal open / close ─────────────────────────────────────────────────────

function openAddModal(): void {
  modalMode.value = 'add'
  editingId.value = null
  form.value = {
    name: '',
    host: '',
    port: 22,
    user: '',
    credentialId: sshCredentials.value[0]?.id ?? '',
  }
  clearFormErrors()
  formBanner.value = ''
  modalOpen.value = true
}

function openEditModal(s: Server): void {
  modalMode.value = 'edit'
  editingId.value = s.id
  form.value = {
    name: s.name,
    host: s.host,
    port: s.port,
    user: s.user,
    credentialId: s.credentialId,
  }
  clearFormErrors()
  formBanner.value = ''
  modalOpen.value = true
}

function closeModal(): void {
  if (formSubmitting.value) return
  modalOpen.value = false
}

// ─── form validation ─────────────────────────────────────────────────────────

function clearFormErrors(): void {
  formErrors.value = { name: '', host: '', port: '', user: '', credentialId: '' }
}

function validateForm(): boolean {
  clearFormErrors()
  let ok = true
  if (!form.value.name.trim()) {
    formErrors.value.name = '请输入服务器名称'
    ok = false
  }
  if (!form.value.host.trim()) {
    formErrors.value.host = '请输入主机地址'
    ok = false
  }
  if (!form.value.user.trim()) {
    formErrors.value.user = '请输入登录用户'
    ok = false
  }
  if (!Number.isInteger(form.value.port) || form.value.port < 1 || form.value.port > 65535) {
    formErrors.value.port = '端口必须在 1..65535 之间'
    ok = false
  }
  if (!form.value.credentialId) {
    formErrors.value.credentialId = '请选择 SSH 凭据'
    ok = false
  }
  return ok
}

// ─── form submit ─────────────────────────────────────────────────────────────

async function handleFormSubmit(): Promise<void> {
  if (!validateForm()) return
  formSubmitting.value = true
  formBanner.value = ''
  try {
    if (modalMode.value === 'add') {
      const payload: CreateServerInput = {
        name: form.value.name.trim(),
        host: form.value.host.trim(),
        port: form.value.port,
        user: form.value.user.trim(),
        credentialId: form.value.credentialId,
      }
      const created = await createServer(payload)
      servers.value = [created, ...servers.value]
    } else if (editingId.value) {
      const payload: UpdateServerInput = {
        name: form.value.name.trim(),
        host: form.value.host.trim(),
        port: form.value.port,
        user: form.value.user.trim(),
        credentialId: form.value.credentialId,
      }
      const updated = await updateServer(editingId.value, payload)
      servers.value = servers.value.map((s) => (s.id === updated.id ? updated : s))
      delete testResults.value[updated.id]
    }
    modalOpen.value = false
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        formBanner.value = '无法连接到服务器,请稍后重试'
      } else {
        formBanner.value = err.apiError?.message ?? `保存失败(${err.status})`
      }
    } else {
      formBanner.value = '保存失败,请稍后重试'
    }
  } finally {
    formSubmitting.value = false
  }
}

// ─── delete ──────────────────────────────────────────────────────────────────

function openDeleteModal(s: Server): void {
  deletingServer.value = s
  deleteBanner.value = ''
  deleteModalOpen.value = true
}

function closeDeleteModal(): void {
  if (deleteSubmitting.value) return
  deleteModalOpen.value = false
  deletingServer.value = null
}

async function confirmDelete(): Promise<void> {
  if (!deletingServer.value) return
  deleteSubmitting.value = true
  deleteBanner.value = ''
  const id = deletingServer.value.id
  try {
    await deleteServer(id)
    servers.value = servers.value.filter((s) => s.id !== id)
    delete testResults.value[id]
    deleteModalOpen.value = false
    deletingServer.value = null
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        deleteBanner.value = '无法连接到服务器,请稍后重试'
      } else {
        deleteBanner.value = err.apiError?.message ?? `删除失败(${err.status})`
      }
    } else {
      deleteBanner.value = '删除失败,请稍后重试'
    }
  } finally {
    deleteSubmitting.value = false
  }
}

// ─── test connection ──────────────────────────────────────────────────────────

async function handleTest(s: Server): Promise<void> {
  testingId.value = s.id
  try {
    const result = await testServer(s.id)
    testResults.value = { ...testResults.value, [s.id]: result }
  } catch (err) {
    let message = '测试连接失败,请稍后重试'
    if (err instanceof HttpError) {
      message = err.apiError?.message ?? `测试连接失败(${err.status})`
    }
    testResults.value = {
      ...testResults.value,
      [s.id]: { ok: false, latencyMs: 0, output: '', error: message },
    }
  } finally {
    if (testingId.value === s.id) testingId.value = null
  }
}
</script>

<template>
  <div class="servers-root">
    <!-- ─── section header ──────────────────────────────────────────────────── -->
    <div class="section-head">
      <div class="section-head-text">
        <h2 class="section-title">服务器</h2>
        <p class="section-desc">
          登记目标服务器(host / port / user + SSH 凭据),测试连通性。SSH 密钥仅按引用绑定,
          经保险库密文存取,绝不入库或回显明文。部署与运维都经此共享 SSH 层执行命令。
        </p>
      </div>
      <button
        class="btn-primary"
        :disabled="loadState === 'loading' || !hasSSHCredentials"
        :title="!hasSSHCredentials ? '请先在凭据保险库添加一条 SSH 私钥凭据' : ''"
        @click="openAddModal"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
          <path d="M12 5v14M5 12h14" />
        </svg>
        登记服务器
      </button>
    </div>

    <!-- ─── no-credential hint ────────────────────────────────────────────────── -->
    <div v-if="loadState === 'idle' && !hasSSHCredentials" class="banner banner--warn" role="status">
      <span>尚无 SSH 凭据。请先到「凭据保险库」添加一条 ssh_key 类型凭据,再登记服务器。</span>
    </div>

    <!-- ─── load error banner ─────────────────────────────────────────────────── -->
    <div v-if="loadState === 'error'" class="banner banner--error" role="alert">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
      </svg>
      <span>{{ loadError }}</span>
      <button class="banner-retry" @click="loadServers">↻ 重试</button>
    </div>

    <!-- ─── servers panel ─────────────────────────────────────────────────────── -->
    <div class="panel" :class="{ 'panel--loading': loadState === 'loading' }">
      <div class="panel-head">
        <span>已登记服务器</span>
        <span class="panel-meta" v-if="loadState === 'idle'">{{ servers.length }} 台</span>
      </div>

      <template v-if="loadState === 'loading'">
        <div class="skel-row" v-for="i in 3" :key="i" aria-hidden="true">
          <div class="skel-bar" />
        </div>
      </template>

      <template v-else-if="loadState === 'idle' && servers.length === 0">
        <div class="empty-row" role="status">还没有登记任何服务器。</div>
      </template>

      <ul v-else class="server-list">
        <li v-for="s in servers" :key="s.id" class="server-row">
          <div class="server-main">
            <div class="server-name">{{ s.name }}</div>
            <div class="server-addr">
              <span class="mono">{{ s.user }}@{{ s.host }}:{{ s.port }}</span>
              <span class="cred-tag">🔑 {{ credentialLabel(s.credentialId) }}</span>
            </div>
            <!-- test result -->
            <div
              v-if="testResults[s.id]"
              class="test-result"
              :class="testResults[s.id].ok ? 'test-result--ok' : 'test-result--fail'"
              role="status"
            >
              <template v-if="testResults[s.id].ok">
                ✓ 连接成功 · {{ testResults[s.id].latencyMs }}ms
                <span class="mono uname">{{ testResults[s.id].output }}</span>
              </template>
              <template v-else>
                ✕ {{ testResults[s.id].error }}
              </template>
            </div>
          </div>
          <div class="server-actions">
            <button class="btn-ghost" :disabled="testingId === s.id" @click="handleTest(s)">
              {{ testingId === s.id ? '测试中…' : '测试连接' }}
            </button>
            <button class="btn-ghost" @click="openLogsModal(s)">日志</button>
            <button class="btn-ghost" @click="openEditModal(s)">编辑</button>
            <button class="btn-ghost btn-danger" @click="openDeleteModal(s)">删除</button>
          </div>
        </li>
      </ul>
    </div>

    <!-- ─── add / edit modal ──────────────────────────────────────────────────── -->
    <div v-if="modalOpen" class="modal-backdrop" @click.self="closeModal">
      <div class="modal" role="dialog" aria-modal="true" aria-labelledby="server-modal-title">
        <h3 id="server-modal-title" class="modal-title">
          {{ modalMode === 'add' ? '登记服务器' : '编辑服务器' }}
        </h3>

        <div v-if="formBanner" class="banner banner--error" role="alert">{{ formBanner }}</div>

        <form @submit.prevent="handleFormSubmit">
          <label class="field">
            <span class="field-label">名称</span>
            <input v-model="form.name" class="field-input" type="text" placeholder="web-prod-1" autocomplete="off" />
            <span v-if="formErrors.name" class="field-error">{{ formErrors.name }}</span>
          </label>

          <div class="field-row">
            <label class="field field-grow">
              <span class="field-label">主机 / IP</span>
              <input v-model="form.host" class="field-input" type="text" placeholder="10.0.0.5" autocomplete="off" />
              <span v-if="formErrors.host" class="field-error">{{ formErrors.host }}</span>
            </label>
            <label class="field field-port">
              <span class="field-label">端口</span>
              <input v-model.number="form.port" class="field-input" type="number" min="1" max="65535" />
              <span v-if="formErrors.port" class="field-error">{{ formErrors.port }}</span>
            </label>
          </div>

          <label class="field">
            <span class="field-label">登录用户</span>
            <input v-model="form.user" class="field-input" type="text" placeholder="deploy" autocomplete="off" />
            <span v-if="formErrors.user" class="field-error">{{ formErrors.user }}</span>
          </label>

          <label class="field">
            <span class="field-label">SSH 凭据</span>
            <select v-model="form.credentialId" class="field-input">
              <option value="" disabled>请选择 SSH 凭据</option>
              <option v-for="c in sshCredentials" :key="c.id" :value="c.id">{{ c.name }}</option>
            </select>
            <span v-if="formErrors.credentialId" class="field-error">{{ formErrors.credentialId }}</span>
          </label>

          <div class="modal-actions">
            <button type="button" class="btn-ghost" :disabled="formSubmitting" @click="closeModal">取消</button>
            <button type="submit" class="btn-primary" :disabled="formSubmitting">
              {{ formSubmitting ? '保存中…' : '保存' }}
            </button>
          </div>
        </form>
      </div>
    </div>

    <!-- ─── delete confirm modal ──────────────────────────────────────────────── -->
    <div v-if="deleteModalOpen" class="modal-backdrop" @click.self="closeDeleteModal">
      <div class="modal" role="dialog" aria-modal="true" aria-labelledby="server-del-title">
        <h3 id="server-del-title" class="modal-title">删除服务器</h3>
        <div v-if="deleteBanner" class="banner banner--error" role="alert">{{ deleteBanner }}</div>
        <p class="modal-text">
          确定删除服务器 <strong>{{ deletingServer?.name }}</strong>?此操作不可撤销。
        </p>
        <div class="modal-actions">
          <button type="button" class="btn-ghost" :disabled="deleteSubmitting" @click="closeDeleteModal">取消</button>
          <button type="button" class="btn-primary btn-danger" :disabled="deleteSubmitting" @click="confirmDelete">
            {{ deleteSubmitting ? '删除中…' : '确认删除' }}
          </button>
        </div>
      </div>
    </div>

    <!-- ─── service logs modal (Story 6-2, FR-16) ─────────────────────────────── -->
    <div v-if="logsModalOpen" class="modal-backdrop" @click.self="closeLogsModal">
      <div class="modal modal--wide" role="dialog" aria-modal="true" aria-labelledby="server-logs-title">
        <div class="logs-modal-head">
          <h3 id="server-logs-title" class="modal-title">
            服务日志 · {{ logsServer?.name }}
          </h3>
          <button type="button" class="btn-ghost" aria-label="关闭" @click="closeLogsModal">关闭</button>
        </div>
        <p class="logs-modal-sub mono">
          {{ logsServer?.user }}@{{ logsServer?.host }}:{{ logsServer?.port }}
        </p>
        <ServiceLogViewer
          v-if="logsServer"
          :server-id="logsServer.id"
          :server-name="logsServer.name"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.servers-root {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.section-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 24px;
}

.section-title {
  font-size: var(--text-heading);
  font-weight: 600;
  color: var(--color-text);
}

.section-desc {
  font-size: var(--text-label);
  color: var(--color-faint);
  margin-top: 6px;
  max-width: 60ch;
  line-height: 1.55;
}

.btn-primary {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  font-size: var(--text-label);
  font-weight: 600;
  color: #fff;
  background: var(--color-primary);
  border: none;
  border-radius: var(--radius-md);
  cursor: pointer;
  white-space: nowrap;
  transition: filter var(--duration-fast);
}
.btn-primary:hover:not(:disabled) {
  filter: brightness(1.08);
}
.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-ghost {
  padding: 6px 12px;
  font-size: var(--text-label);
  font-weight: 500;
  color: var(--color-dim);
  background: transparent;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition:
    color var(--duration-fast),
    border-color var(--duration-fast),
    background var(--duration-fast);
}
.btn-ghost:hover:not(:disabled) {
  color: var(--color-text);
  border-color: var(--color-dim);
}
.btn-ghost:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.btn-danger {
  color: var(--color-danger, #d4503e);
}
.btn-danger:hover:not(:disabled) {
  border-color: var(--color-danger, #d4503e);
  color: var(--color-danger, #d4503e);
}
.btn-primary.btn-danger {
  color: #fff;
  background: var(--color-danger, #d4503e);
}

.banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  font-size: var(--text-label);
  border-radius: var(--radius-md);
}
.banner--error {
  color: var(--color-danger, #d4503e);
  background: color-mix(in oklch, var(--color-danger, #d4503e) 10%, transparent);
}
.banner--warn {
  color: var(--color-warn, #b88600);
  background: color-mix(in oklch, var(--color-warn, #b88600) 12%, transparent);
}
.banner-retry {
  margin-left: auto;
  background: none;
  border: none;
  color: inherit;
  font-weight: 600;
  cursor: pointer;
}

.panel {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: var(--color-surface);
  overflow: hidden;
}
.panel--loading {
  opacity: 0.7;
}
.panel-head {
  display: flex;
  justify-content: space-between;
  padding: 12px 18px;
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-dim);
  border-bottom: 1px solid var(--color-border);
}
.panel-meta {
  color: var(--color-faint);
  font-weight: 500;
}

.skel-row {
  padding: 16px 18px;
}
.skel-bar {
  height: 16px;
  border-radius: 4px;
  background: linear-gradient(90deg, var(--color-border) 25%, transparent 50%, var(--color-border) 75%);
  background-size: 200% 100%;
  animation: skel 1.4s ease-in-out infinite;
}
@keyframes skel {
  0% {
    background-position: 200% 0;
  }
  100% {
    background-position: -200% 0;
  }
}

.empty-row {
  padding: 28px 18px;
  text-align: center;
  font-size: var(--text-label);
  color: var(--color-faint);
}

.server-list {
  list-style: none;
}
.server-row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  padding: 16px 18px;
  border-bottom: 1px solid var(--color-border);
}
.server-row:last-child {
  border-bottom: none;
}
.server-main {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.server-name {
  font-size: var(--text-body);
  font-weight: 600;
  color: var(--color-text);
}
.server-addr {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  font-size: var(--text-label);
  color: var(--color-faint);
}
.mono {
  font-family: var(--font-mono, ui-monospace, monospace);
}
.cred-tag {
  color: var(--color-dim);
}
.test-result {
  margin-top: 6px;
  font-size: var(--text-label);
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: baseline;
}
.test-result--ok {
  color: var(--color-success, #2e8b57);
}
.test-result--fail {
  color: var(--color-danger, #d4503e);
}
.uname {
  color: var(--color-faint);
  font-size: 0.85em;
  word-break: break-all;
}
.server-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

/* modal */
.modal-backdrop {
  position: fixed;
  inset: 0;
  background: color-mix(in oklch, var(--color-text) 40%, transparent);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  z-index: 100;
}
.modal {
  width: 100%;
  max-width: 440px;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  box-shadow: var(--shadow-lg, 0 24px 60px rgba(0, 0, 0, 0.24));
}
.modal--wide {
  max-width: min(960px, 92vw);
}
.logs-modal-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.logs-modal-sub {
  font-size: 0.78rem;
  color: var(--color-dim);
  margin-top: -8px;
}
.modal-title {
  font-size: var(--text-heading);
  font-weight: 600;
  color: var(--color-text);
}
.modal-text {
  font-size: var(--text-label);
  color: var(--color-dim);
  line-height: 1.5;
}
.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 4px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 14px;
}
.field-row {
  display: flex;
  gap: 12px;
}
.field-grow {
  flex: 1;
}
.field-port {
  width: 96px;
}
.field-label {
  font-size: var(--text-label);
  font-weight: 500;
  color: var(--color-dim);
}
.field-input {
  padding: 8px 12px;
  font-size: var(--text-body);
  color: var(--color-text);
  background: var(--color-bg, var(--color-surface));
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  transition: border-color var(--duration-fast);
}
.field-input:focus {
  outline: none;
  border-color: var(--color-primary);
}
.field-error {
  font-size: var(--text-label);
  color: var(--color-danger, #d4503e);
}
</style>
