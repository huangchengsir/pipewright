<script setup lang="ts">
/**
 * RunnerPanel — 远程构建 runner 选择(FR-8-14 续).
 *
 * 选一台已登记的服务器作该项目的远程构建机:配置后构建下沉到该机执行(控制机本地克隆 → 经 SSH 传
 * 工作区 → 远程容器跑;token 只在控制机)。「本地构建」= 不下沉(默认)。自包含 load/save。
 */
import { ref, onMounted, watch } from 'vue'
import { getRunner, saveRunner } from '../api/runner'
import { listServers, type Server } from '../api/servers'
import { HttpError } from '../api/http'

const props = defineProps<{ projectId: string }>()

type LoadState = 'idle' | 'loading' | 'error'
const loadState = ref<LoadState>('idle')
const loadError = ref('')

const servers = ref<Server[]>([])
const selected = ref('') // '' = 本地构建
const saveSubmitting = ref(false)
const saveBanner = ref('')
const saveSuccess = ref(false)

async function load(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    const [cfg, srv] = await Promise.all([getRunner(props.projectId), listServers().catch(() => [])])
    selected.value = cfg.runnerServerId
    servers.value = srv
    loadState.value = 'idle'
  } catch (err) {
    loadState.value = 'error'
    loadError.value = err instanceof HttpError ? err.message : '加载 runner 配置失败'
  }
}

async function handleSave(): Promise<void> {
  saveBanner.value = ''
  saveSuccess.value = false
  saveSubmitting.value = true
  try {
    const cfg = await saveRunner(props.projectId, selected.value)
    selected.value = cfg.runnerServerId
    saveSuccess.value = true
    saveBanner.value = selected.value ? '已设为远程构建' : '已设为本地构建'
  } catch (err) {
    saveSuccess.value = false
    saveBanner.value =
      err instanceof HttpError ? (err.apiError?.message ?? '保存失败') : '保存失败,请重试'
  } finally {
    saveSubmitting.value = false
  }
}

onMounted(load)
watch(() => props.projectId, load)
</script>

<template>
  <section class="config-card" aria-labelledby="runner-heading">
    <div class="card-head">
      <span class="card-icon" aria-hidden="true">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
          <rect x="2" y="4" width="20" height="6" rx="1" /><rect x="2" y="14" width="20" height="6" rx="1" />
          <path d="M6 7h.01M6 17h.01" />
        </svg>
      </span>
      <h2 id="runner-heading" class="card-title">构建 runner</h2>
      <span class="card-sub">把构建下沉到远程构建机执行(代码经 SSH 传输,token 只在中控机)</span>
    </div>
    <div class="card-body card-body--pad">
      <p v-if="loadState === 'loading'" class="runner-loading">加载中…</p>
      <p v-else-if="loadState === 'error'" class="runner-error" role="alert">{{ loadError }}</p>
      <template v-else>
        <label class="runner-field-label" for="runner-select">构建在哪执行</label>
        <select id="runner-select" v-model="selected" class="runner-select" @change="saveSuccess = false">
          <option value="">本地构建(中控机)</option>
          <option v-for="s in servers" :key="s.id" :value="s.id">远程:{{ s.name }}({{ s.host }})</option>
        </select>
        <p class="runner-hint">
          远程构建需该机装有容器运行时(docker/nerdctl/podman)。仅 script 类型 job 在远程执行;
          测试报告/质量门禁在远程模式暂不回采(后续增量)。
        </p>
        <p
          v-if="saveBanner"
          class="runner-banner"
          :class="saveSuccess ? 'runner-banner--ok' : 'runner-banner--err'"
          role="status"
        >{{ saveBanner }}</p>
        <div class="runner-save">
          <button class="btn-primary" :disabled="saveSubmitting" :aria-busy="saveSubmitting" @click="handleSave">
            <span v-if="saveSubmitting" class="spinner" aria-hidden="true" />
            {{ saveSubmitting ? '保存中…' : '保存 runner 配置' }}
          </button>
        </div>
      </template>
    </div>
  </section>
</template>

<style scoped>
.config-card {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg, 12px);
  background: var(--color-card);
  overflow: hidden;
}
.card-head {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--color-border);
}
.card-icon {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: var(--rounded-md);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  flex: none;
}
.card-title { font-size: 0.92rem; font-weight: 650; color: var(--color-text); }
.card-sub { font-size: 0.76rem; color: var(--color-faint); flex: 1; min-width: 0; }
.card-body--pad { padding: 16px; display: flex; flex-direction: column; gap: 12px; }
.runner-loading, .runner-error { margin: 0; font-size: 0.82rem; }
.runner-error { color: var(--color-danger, #dc2626); }
.runner-loading { color: var(--color-faint); }
.runner-field-label { font-size: 0.8rem; font-weight: 600; color: var(--color-text); }
.runner-select {
  width: 100%;
  height: 36px;
  padding: 0 11px;
  font: inherit;
  font-size: 0.85rem;
  color: var(--color-text);
  background: var(--color-bg-subtle, var(--color-card));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  box-sizing: border-box;
}
.runner-select:focus { outline: none; border-color: var(--color-primary); }
.runner-hint { margin: 0; font-size: 0.7rem; color: var(--color-faint); line-height: 1.45; }
.runner-banner { margin: 0; font-size: 0.8rem; font-weight: 500; }
.runner-banner--ok { color: var(--color-success, #16a34a); }
.runner-banner--err { color: var(--color-danger, #dc2626); }
.runner-save { display: flex; justify-content: flex-end; }
</style>
