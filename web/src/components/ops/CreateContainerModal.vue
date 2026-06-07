<script setup lang="ts">
/*
  CreateContainerModal.vue — 新增容器(docker run -d)。
  表单收集镜像/名/端口/env/卷/重启/命令 → POST /api/servers/:id/containers。
  端口/env/卷用多行文本框(一行一条),空行忽略。后端严格白名单校验,绝不拼 shell。
*/
import { ref, computed } from 'vue'
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

const toast = useToast()

const serverId = ref(props.servers[0]?.id ?? '')
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
async function aiGenerate(): Promise<void> {
  const nl = aiNl.value.trim()
  if (!nl || aiBusy.value) return
  aiBusy.value = true
  aiNote.value = ''
  banner.value = ''
  try {
    const res = await suggestCommand(nl, { os: 'linux', shell: '/bin/sh', container: '(docker 主机)' })
    if (!res.available) {
      aiNote.value = 'AI 未配置:请到「设置 › AI」配置模型后再用生成。'
      return
    }
    const parsed = parseDockerRun(res.command)
    if (!parsed) {
      aiNote.value = `AI 给的不是可解析的 docker run,已显示原文供参考:${res.command}`
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
    aiNote.value = `已按 AI 建议填表 · 原命令:${res.command}`
    toast.success('AI 已生成配置', { detail: '请核对后再创建' })
  } catch (err) {
    aiNote.value = err instanceof HttpError ? (err.apiError?.message ?? `生成失败(${err.status})`) : '生成请求失败'
  } finally {
    aiBusy.value = false
  }
}

const RESTART_OPTIONS: { value: RestartPolicy; label: string }[] = [
  { value: 'no', label: '不自动重启 (no)' },
  { value: 'unless-stopped', label: '除非手动停止 (unless-stopped)' },
  { value: 'always', label: '总是重启 (always)' },
  { value: 'on-failure', label: '失败时重启 (on-failure)' },
]

const canSubmit = computed(() => serverId.value !== '' && image.value.trim() !== '' && !submitting.value)

/** 多行文本 → 去空行、去首尾空白的数组。 */
function linesToArray(text: string): string[] {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l !== '')
}

async function submit(): Promise<void> {
  if (!canSubmit.value) return
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
    const res = await createContainer(serverId.value, input)
    if (res.ok) {
      toast.success('容器已创建', { detail: `${name.value || image.value} · ${res.containerId.slice(0, 12)}` })
      emit('created', serverId.value)
      emit('close')
    } else {
      banner.value = res.error || 'docker run 失败'
    }
  } catch (err) {
    banner.value =
      err instanceof HttpError ? (err.apiError?.message ?? `创建失败(${err.status})`) : '创建请求失败'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="modal-scrim" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-label="新增容器">
      <header class="modal__head">
        <h3 class="modal__title">新增容器</h3>
        <button class="modal__close" aria-label="关闭" @click="emit('close')">✕</button>
      </header>

      <div class="modal__body">
        <p v-if="banner" class="form-banner">⚠ {{ banner }}</p>

        <!-- AI 生成:中文描述 → 自动填表 -->
        <div class="ai-gen">
          <span class="ai-gen__spark">✦</span>
          <input
            v-model="aiNl"
            class="ai-gen__in"
            placeholder="用中文描述,如:起个 nginx 映射 8080,挂载 /data/web,自动重启"
            @keyup.enter="aiGenerate"
          />
          <button class="ai-gen__btn" :disabled="aiBusy || !aiNl.trim()" @click="aiGenerate">
            {{ aiBusy ? '生成中…' : 'AI 生成' }}
          </button>
        </div>
        <p v-if="aiNote" class="ai-gen__note">{{ aiNote }}</p>

        <div class="grid2">
          <label class="field">
            <span class="field__k">目标服务器</span>
            <select v-model="serverId" class="field__in">
              <option v-for="s in props.servers" :key="s.id" :value="s.id">{{ s.name }}</option>
            </select>
          </label>
          <label class="field">
            <span class="field__k">重启策略</span>
            <select v-model="restart" class="field__in">
              <option v-for="o in RESTART_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
          </label>
        </div>

        <div class="grid2">
          <label class="field">
            <span class="field__k">镜像 <em>*</em></span>
            <input v-model="image" class="field__in mono" placeholder="例:nginx:latest" />
          </label>
          <label class="field">
            <span class="field__k">容器名(可选)</span>
            <input v-model="name" class="field__in mono" placeholder="例:my-web" />
          </label>
        </div>

        <label class="field">
          <span class="field__k">端口映射 <span class="field__hint">一行一条,host:container</span></span>
          <textarea v-model="portsText" class="field__in mono" rows="2" placeholder="8080:80&#10;127.0.0.1:9090:90/tcp" />
        </label>

        <label class="field">
          <span class="field__k">环境变量 <span class="field__hint">一行一条,KEY=VALUE</span></span>
          <textarea v-model="envText" class="field__in mono" rows="2" placeholder="TZ=Asia/Shanghai&#10;FOO=bar" />
        </label>

        <label class="field">
          <span class="field__k">数据卷 <span class="field__hint">一行一条,host:container[:ro]</span></span>
          <textarea v-model="volumesText" class="field__in mono" rows="2" placeholder="/data/web:/usr/share/nginx/html:ro&#10;myvol:/cache" />
        </label>

        <label class="field">
          <span class="field__k">启动命令(可选)</span>
          <input v-model="command" class="field__in mono" placeholder="覆盖镜像默认 CMD,按空格拆参数" />
        </label>
      </div>

      <footer class="modal__foot">
        <span class="foot-hint">将在所选服务器执行 docker run -d</span>
        <span class="grow" />
        <button class="btn btn--ghost" :disabled="submitting" @click="emit('close')">取消</button>
        <button class="btn btn--primary" :disabled="!canSubmit" @click="submit">
          {{ submitting ? '创建中…' : '创建并运行' }}
        </button>
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
