<script setup lang="ts">
/*
  CreateContainerModal.vue — 新增容器(docker run -d)。
  表单收集镜像/名/端口/env/卷/重启/命令 → POST /api/servers/:id/containers。
  端口/env/卷用多行文本框(一行一条),空行忽略。后端严格白名单校验,绝不拼 shell。
*/
import { ref, computed, watch } from 'vue'
import PermissionGate from '../PermissionGate.vue'
import { usePermission, usePermissions } from '../../composables/usePermission'
import { useIdentityRequestScope } from '../../composables/useAuthorizedRequestScope'
import { useI18n } from 'vue-i18n'
import { createContainer, type CreateContainerInput, type RestartPolicy } from '../../api/containers'
import { suggestCommand } from '../../api/aiOps'
import { parseDockerRun } from '../../lib/dockerRun'
import { HttpError } from '../../api/http'
import { useToast } from '../../composables/useToast'

interface ServerOption {
  id: string
  name: string
}
const props = defineProps<{ servers: ServerOption[] }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'created', serverId: string): void }>()

const { t } = useI18n()
const toast = useToast()

const serverId = ref(props.servers[0]?.id ?? '')
const operationQuery=computed(()=>({action:'container.operate',resourceId:serverId.value}))
const operationPermission = usePermission(operationQuery)
const hasSelectedServer = computed(() => props.servers.some(server => server.id === serverId.value))
const aiQueries = computed(() => [operationQuery.value, {action:'container.diagnose',resourceId:serverId.value}])
const aiPermission = usePermissions(aiQueries)
const image = ref('')
const name = ref('')
const portsText = ref('')
const envText = ref('')
const volumesText = ref('')
const restart = ref<RestartPolicy>('unless-stopped')
const command = ref('')

const submitting = ref(false)
const banner = ref('')

// ─── AI 生成配置:中文描述 → docker run → 解析填表 ──────────────────────────────
const aiNl = ref('')
const aiBusy = ref(false)
const aiNote = ref('')
let createRequest: AbortController | undefined
let aiRequest: AbortController | undefined
const closed = ref(false)
function resetPending() {
  createRequest?.abort()
  aiRequest?.abort()
  createRequest = aiRequest = undefined
  submitting.value = aiBusy.value = false
  aiNote.value = banner.value = ''
}
const scope = useIdentityRequestScope(() => {
  resetPending()
  image.value = name.value = portsText.value = envText.value = volumesText.value = command.value = aiNl.value = ''
})
const canOperate = () => !closed.value && scope.active() && hasSelectedServer.value && operationPermission.allowed.value
const canGenerate = () => canOperate() && aiPermission.allowed.value
watch([serverId, hasSelectedServer], resetPending, {flush:'sync'})
watch(operationPermission.allowed, (allowed) => { if (!allowed) resetPending() }, {flush:'sync'})
watch(aiPermission.allowed, (allowed) => {
  if (!allowed) {
    aiRequest?.abort()
    aiRequest = undefined
    aiBusy.value = false
    aiNote.value = ''
  }
}, {flush:'sync'})
function dismiss() {
  closed.value = true
  resetPending()
  emit('close')
}
async function aiGenerate(): Promise<void> {
  const nl = aiNl.value.trim()
  if (!nl || aiBusy.value || !canGenerate()) return
  const target = serverId.value
  const request = new AbortController()
  aiRequest = request
  const current = () => aiRequest === request && target === serverId.value && canGenerate()
  aiBusy.value = true
  aiNote.value = ''
  banner.value = ''
  try {
    const res = await suggestCommand(nl, { os: 'linux', shell: '/bin/sh', container: t('opsContainer.create.dockerHost') }, { ...scope.requestOptions, signal: request.signal })
    if (!current()) return
    if (!res.available) {
      aiNote.value = t('opsContainer.create.aiNotConfigured')
      return
    }
    const parsed = parseDockerRun(res.command)
    if (!parsed) {
      aiNote.value = t('opsContainer.create.aiNotParsable', { command: res.command })
      return
    }
    // 填表(只覆盖 AI 给出的部分;空的保持)。
    if (parsed.image) image.value = parsed.image
    if (parsed.name) name.value = parsed.name
    if (parsed.ports.length) portsText.value = parsed.ports.join('\n')
    if (parsed.env.length) envText.value = parsed.env.join('\n')
    if (parsed.volumes.length) volumesText.value = parsed.volumes.join('\n')
    if (parsed.restart) restart.value = parsed.restart
    if (parsed.command) command.value = parsed.command
    aiNote.value = t('opsContainer.create.aiFilled', { command: res.command })
    toast.success(t('opsContainer.create.aiGenerated'), { detail: t('opsContainer.create.aiGeneratedDetail') })
  } catch (err) {
    if (!current()) return
    aiNote.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? t('opsContainer.create.genFailedStatus', { status: err.status }))
        : t('opsContainer.create.genFailed')
  } finally {
    if (aiRequest === request) {
      aiRequest = undefined
      aiBusy.value = false
    }
  }
}

const RESTART_OPTIONS = computed<{ value: RestartPolicy; label: string }[]>(() => [
  { value: 'no', label: t('opsContainer.create.restartNo') },
  { value: 'unless-stopped', label: t('opsContainer.create.restartUnlessStopped') },
  { value: 'always', label: t('opsContainer.create.restartAlways') },
  { value: 'on-failure', label: t('opsContainer.create.restartOnFailure') },
])

const canSubmit = computed(() => canOperate() && image.value.trim() !== '' && !submitting.value)

/** 多行文本 → 去空行、去首尾空白的数组。 */
function linesToArray(text: string): string[] {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l !== '')
}

async function submit(): Promise<void> {
  if (!canSubmit.value) return
  const target = serverId.value
  const request = new AbortController()
  createRequest = request
  const current = () => createRequest === request && target === serverId.value && canOperate()
  banner.value = ''
  submitting.value = true
  const input: CreateContainerInput = {
    image: image.value.trim(),
    name: name.value.trim() || undefined,
    ports: linesToArray(portsText.value),
    env: linesToArray(envText.value),
    volumes: linesToArray(volumesText.value),
    restart: restart.value,
    command: command.value.trim() || undefined,
  }
  try {
    const res = await createContainer(target, input, { ...scope.requestOptions, signal: request.signal })
    if (!current()) return
    if (res.ok) {
      toast.success(t('opsContainer.create.created'), { detail: `${input.name || input.image} · ${res.containerId.slice(0, 12)}` })
      emit('created', target)
      dismiss()
    } else {
      banner.value = res.error || t('opsContainer.create.dockerRunFailed')
    }
  } catch (err) {
    if (!current()) return
    banner.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? t('opsContainer.create.createFailedStatus', { status: err.status }))
        : t('opsContainer.create.createFailed')
  } finally {
    if (createRequest === request) {
      createRequest = undefined
      submitting.value = false
    }
  }
}
</script>

<template>
  <div class="modal-scrim" @click.self="dismiss">
    <div class="modal" role="dialog" :aria-label="t('opsContainer.create.title')">
      <header class="modal__head">
        <h3 class="modal__title">{{ t('opsContainer.create.title') }}</h3>
        <button class="modal__close" :aria-label="t('opsContainer.close')" @click="dismiss">✕</button>
      </header>

      <div class="modal__body">
        <p v-if="banner" class="form-banner">⚠ {{ banner }}</p>

        <!-- AI 生成:中文描述 → 自动填表 -->
        <div v-if="canGenerate()" class="ai-gen">
          <span class="ai-gen__spark">✦</span>
          <input
            v-model="aiNl"
            class="ai-gen__in"
            :placeholder="t('opsContainer.create.aiPlaceholder')"
            @keyup.enter="aiGenerate"
          />
          <button class="ai-gen__btn" :disabled="aiBusy || !aiNl.trim()" @click="aiGenerate">
            {{ aiBusy ? t('opsContainer.create.generating') : t('opsContainer.create.aiGenerate') }}
          </button>
        </div>
        <p v-if="aiNote" class="ai-gen__note">{{ aiNote }}</p>

        <div class="grid2">
          <label class="field">
            <span class="field__k">{{ t('opsContainer.create.targetServer') }}</span>
            <select v-model="serverId" class="field__in">
              <option v-for="s in props.servers" :key="s.id" :value="s.id">{{ s.name }}</option>
            </select>
          </label>
          <label class="field">
            <span class="field__k">{{ t('opsContainer.create.restartPolicy') }}</span>
            <select v-model="restart" class="field__in">
              <option v-for="o in RESTART_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
          </label>
        </div>

        <div class="grid2">
          <label class="field">
            <span class="field__k">{{ t('opsContainer.create.image') }} <em>*</em></span>
            <input v-model="image" class="field__in mono" :placeholder="t('opsContainer.create.imagePlaceholder')" />
          </label>
          <label class="field">
            <span class="field__k">{{ t('opsContainer.create.name') }}</span>
            <input v-model="name" class="field__in mono" :placeholder="t('opsContainer.create.namePlaceholder')" />
          </label>
        </div>

        <label class="field">
          <span class="field__k">{{ t('opsContainer.create.ports') }} <span class="field__hint">{{ t('opsContainer.create.portsHint') }}</span></span>
          <textarea v-model="portsText" class="field__in mono" rows="2" placeholder="8080:80&#10;127.0.0.1:9090:90/tcp" />
        </label>

        <label class="field">
          <span class="field__k">{{ t('opsContainer.create.env') }} <span class="field__hint">{{ t('opsContainer.create.envHint') }}</span></span>
          <textarea v-model="envText" class="field__in mono" rows="2" placeholder="TZ=Asia/Shanghai&#10;FOO=bar" />
        </label>

        <label class="field">
          <span class="field__k">{{ t('opsContainer.create.volumes') }} <span class="field__hint">{{ t('opsContainer.create.volumesHint') }}</span></span>
          <textarea v-model="volumesText" class="field__in mono" rows="2" placeholder="/data/web:/usr/share/nginx/html:ro&#10;myvol:/cache" />
        </label>

        <label class="field">
          <span class="field__k">{{ t('opsContainer.create.command') }}</span>
          <input v-model="command" class="field__in mono" :placeholder="t('opsContainer.create.commandPlaceholder')" />
        </label>
      </div>

      <footer class="modal__foot">
        <span class="foot-hint">{{ t('opsContainer.create.footHint') }}</span>
        <span class="grow" />
        <button class="btn btn--ghost" :disabled="submitting" @click="dismiss">{{ t('opsContainer.cancel') }}</button>
        <PermissionGate :query="operationQuery"><button class="btn btn--primary" :disabled="!canSubmit" @click="submit">
          {{ submitting ? t('opsContainer.create.creating') : t('opsContainer.create.createRun') }}
        </button></PermissionGate>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.modal-scrim {
  position: fixed;
  inset: 0;
  z-index: 500;
  background: oklch(0% 0 0 / 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  animation: scrimIn var(--duration-fast) var(--ease-out-expo);
}
@keyframes scrimIn {
  from { opacity: 0; }
  to { opacity: 1; }
}
.modal {
  width: min(640px, 96vw);
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-lg, 12px);
  box-shadow: var(--shadow-modal);
  animation: modalIn var(--duration-normal) var(--ease-out-expo);
}
@keyframes modalIn {
  from { transform: translateY(12px) scale(0.99); opacity: 0.5; }
  to { transform: translateY(0) scale(1); opacity: 1; }
}
.modal__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--color-border);
}
.modal__title {
  margin: 0;
  font-size: var(--text-h1);
  font-weight: 700;
  color: var(--color-text);
}
.modal__close {
  width: 28px;
  height: 28px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
}
.modal__close:hover {
  color: var(--color-text);
  border-color: var(--color-text);
}
.modal__body {
  padding: 18px 20px;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.grid2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
@media (max-width: 560px) {
  .grid2 { grid-template-columns: 1fr; }
}
.field {
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.field__k {
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-dim);
}
.field__k em {
  color: var(--color-red);
  font-style: normal;
}
.field__hint {
  font-weight: 400;
  color: var(--color-faint);
  font-size: var(--text-micro);
}
.field__in {
  font-size: var(--text-body);
  padding: 8px 10px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
  color: var(--color-text);
  resize: vertical;
}
.field__in:focus {
  outline: none;
  border-color: var(--color-primary);
}
.ai-gen {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  border: 1px solid var(--color-primary-soft);
  background: var(--color-primary-soft);
  border-radius: var(--rounded-sm);
}
.ai-gen__spark {
  color: var(--color-primary);
  font-weight: 700;
}
.ai-gen__in {
  flex: 1;
  font-size: var(--text-label);
  padding: 6px 10px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-text);
}
.ai-gen__in:focus {
  outline: none;
  border-color: var(--color-primary);
}
.ai-gen__btn {
  font-size: var(--text-label);
  font-weight: 600;
  padding: 6px 14px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-primary);
  background: var(--color-primary);
  color: #fff;
  cursor: pointer;
  white-space: nowrap;
}
.ai-gen__btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.ai-gen__note {
  margin: 0;
  font-size: var(--text-micro);
  color: var(--color-dim);
  word-break: break-all;
}
.form-banner {
  margin: 0;
  padding: 9px 12px;
  border-radius: var(--rounded-sm);
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  color: var(--color-red);
  font-size: var(--text-label);
}
.modal__foot {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 20px;
  border-top: 1px solid var(--color-border);
}
.foot-hint {
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.grow {
  flex: 1;
}
.btn {
  font-size: var(--text-label);
  font-weight: 600;
  padding: 8px 16px;
  border-radius: var(--rounded-sm);
  border: 1px solid transparent;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out-expo);
}
.btn--ghost {
  background: transparent;
  border-color: var(--color-border-strong);
  color: var(--color-dim);
}
.btn--ghost:hover:not(:disabled) {
  color: var(--color-text);
  border-color: var(--color-text);
}
.btn--primary {
  background: var(--color-primary);
  color: #fff;
}
.btn--primary:hover:not(:disabled) {
  background: var(--color-primary-press);
}
.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
