<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { type ArtifactDTO, type DeployBatch, deployBatch, listDeployBatches, retryDeployBatch, continueDeployBatch } from '../../api/runs'
import { listServers, type Server } from '../../api/servers'
import { HttpError } from '../../api/http'
import AppSelect from '../ui/AppSelect.vue'

const props = defineProps<{ runId: string; artifacts: ArtifactDTO[]; canCreate: boolean }>()
const emit = defineEmits<{ completed: [] }>()
const { t } = useI18n()

interface DraftItem { key: string; artifactId: string; serverIds: string[]; path: string }
const rows = ref<DraftItem[]>([])
const servers = ref<Server[]>([])
const batches = ref<DeployBatch[]>([])
const busy = ref(false)
const error = ref('')
const idempotencyKey = ref('')
let poll: ReturnType<typeof setInterval> | undefined

const fileArtifacts = computed(() => props.artifacts.filter((a) => a.type !== 'image'))
const artifactOptions = computed(() => fileArtifacts.value.map((a) => ({
  value: a.id,
  label: `${a.name} · ${a.type} · ${typeof a.metadata.workspacePath === 'string' ? a.metadata.workspacePath : a.reference.slice(0, 12)}`,
})))

function addRow(): void {
  rows.value.push({ key: crypto.randomUUID(), artifactId: fileArtifacts.value[0]?.id ?? '', serverIds: [], path: '' })
}

function toggleServer(row: DraftItem, id: string): void {
  row.serverIds = row.serverIds.includes(id) ? row.serverIds.filter((value) => value !== id) : [...row.serverIds, id]
}

async function refresh(): Promise<void> {
  const response = await listDeployBatches(props.runId)
  batches.value = response.batches
}

async function load(): Promise<void> {
  try {
    const [available, history] = await Promise.all([listServers(), listDeployBatches(props.runId)])
    servers.value = available
    batches.value = history.batches
  } catch (cause) {
    error.value = message(cause)
  }
}

function message(cause: unknown): string {
  return cause instanceof HttpError ? (cause.apiError?.message ?? t('batchDeploy.requestFailed')) : t('batchDeploy.requestFailed')
}

function validate(): boolean {
  if (!rows.value.length || rows.value.some((row) => !row.artifactId || !row.serverIds.length || !row.path.trim().startsWith('/'))) {
    error.value = t('batchDeploy.completeRows')
    return false
  }
  const occupied = new Map<string, string[]>()
  for (const row of rows.value) {
    const path = row.path.trim().replace(/\/+$/, '')
    for (const serverId of row.serverIds) {
      const paths = occupied.get(serverId) ?? []
      if (paths.some((other) => path === other || path.startsWith(`${other}/`) || other.startsWith(`${path}/`))) {
        error.value = t('batchDeploy.pathConflict')
        return false
      }
      paths.push(path)
      occupied.set(serverId, paths)
    }
  }
  return true
}

async function submit(): Promise<void> {
  if (busy.value || !validate()) return
  busy.value = true
  error.value = ''
  if (!idempotencyKey.value) idempotencyKey.value = crypto.randomUUID()
  try {
    await deployBatch(props.runId, {
      idempotencyKey: idempotencyKey.value,
      items: rows.value.map((row) => ({
        artifactId: row.artifactId,
        serverIds: [...row.serverIds],
        deployConfig: { path: row.path.trim() },
      })),
    })
    idempotencyKey.value = ''
    rows.value = []
    await refresh()
    emit('completed')
  } catch (cause) {
    error.value = message(cause)
    await refresh().catch(() => undefined)
  } finally {
    busy.value = false
  }
}

async function resume(batch: DeployBatch, mode: 'retry' | 'continue'): Promise<void> {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    if (mode === 'retry') await retryDeployBatch(props.runId, batch.id)
    else await continueDeployBatch(props.runId, batch.id)
    await refresh()
    emit('completed')
  } catch (cause) {
    error.value = message(cause)
    await refresh().catch(() => undefined)
  } finally {
    busy.value = false
  }
}

watch(rows, () => { idempotencyKey.value = '' }, { deep: true })
watch(() => props.runId, () => { rows.value = []; batches.value = []; void load() })
onMounted(() => {
  void load()
  poll = setInterval(() => { if (!busy.value) void refresh().catch(() => undefined) }, 5000)
})
onUnmounted(() => { if (poll) clearInterval(poll) })
</script>

<template>
  <section class="batch-deploy" :aria-label="t('batchDeploy.title')">
    <div class="batch-head">
      <h2>{{ t('batchDeploy.title') }}</h2>
      <button v-if="canCreate && fileArtifacts.length" type="button" class="batch-button" :disabled="busy" @click="addRow">+ {{ t('batchDeploy.add') }}</button>
    </div>
    <div v-if="rows.length" class="batch-form">
      <div v-for="(row, index) in rows" :key="row.key" class="batch-row">
        <div class="row-head">
          <strong>{{ t('batchDeploy.item', { n: index + 1 }) }}</strong>
          <button type="button" class="remove-button" :aria-label="t('batchDeploy.remove')" :disabled="busy" @click="rows.splice(index, 1)">×</button>
        </div>
        <div class="row-fields">
          <label>
            <span>{{ t('batchDeploy.artifact') }}</span>
            <AppSelect v-model="row.artifactId" :options="artifactOptions" :aria-label="t('batchDeploy.artifact')" />
          </label>
          <label>
            <span>{{ t('batchDeploy.path') }}</span>
            <input v-model="row.path" type="text" placeholder="/srv/my-app" autocomplete="off" />
          </label>
        </div>
        <div class="server-field">
          <span>{{ t('batchDeploy.servers') }}</span>
          <div class="server-list">
            <label v-for="server in servers" :key="server.id"><input type="checkbox" :checked="row.serverIds.includes(server.id)" @change="toggleServer(row, server.id)" />{{ server.name }}</label>
            <span v-if="!servers.length" class="muted">{{ t('batchDeploy.noServers') }}</span>
          </div>
        </div>
      </div>
      <div class="form-actions"><button type="button" class="submit-button" :disabled="busy" @click="submit">{{ busy ? t('batchDeploy.running') : t('batchDeploy.start') }}</button></div>
    </div>
    <p v-if="error" class="batch-error" role="alert">{{ error }}</p>
    <div v-if="batches.length" class="batch-history">
      <h3>{{ t('batchDeploy.history') }}</h3>
      <div v-for="batch in batches" :key="batch.id" class="history-group">
        <div class="history-head">
          <span>{{ new Date(batch.createdAt).toLocaleString() }} · {{ t(`batchDeploy.status_${batch.status}`) }}</span>
          <div class="history-actions">
            <button v-if="batch.items.some((item) => item.status === 'failed' || item.status === 'rolled_back')" type="button" :disabled="busy" @click="resume(batch, 'retry')">{{ t('batchDeploy.retry') }}</button>
            <button v-if="batch.items.some((item) => item.status === 'pending')" type="button" :disabled="busy" @click="resume(batch, 'continue')">{{ t('batchDeploy.continue') }}</button>
          </div>
        </div>
        <div v-for="item in batch.items" :key="item.id" class="result-row">
          <span class="result-name">{{ item.artifactName }} → {{ item.serverName }}</span>
          <code>{{ item.deployConfig.releaseBase || item.deployConfig.path }}</code>
          <span :class="['result-status', `result-status--${item.status}`]">{{ t(`batchDeploy.status_${item.status}`) }}</span>
          <span v-if="item.message" class="muted">{{ item.message }}</span>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.batch-deploy { margin-top: 24px; padding-top: 22px; border-top: 1px solid #e2e5eb; }
.batch-head,.row-head,.history-head,.form-actions { display:flex; align-items:center; justify-content:space-between; gap:12px; }
h2 { margin:0; font-size:17px; } h3 { margin:0 0 12px; font-size:14px; }
.batch-button,.history-actions button { padding:7px 11px; border:1px solid #cfd5df; border-radius:6px; background:#fff; color:#244a77; cursor:pointer; }
.batch-form { margin-top:16px; }
.batch-row { padding:15px 0; border-top:1px solid #e7e9ee; }
.row-head { margin-bottom:12px; font-size:13px; }
.remove-button { width:30px; height:30px; border:0; background:transparent; color:#687386; font-size:20px; cursor:pointer; }
.row-fields { display:grid; grid-template-columns: minmax(0,1fr) minmax(0,1fr); gap:14px; }
.row-fields label,.server-field { min-width:0; display:flex; flex-direction:column; gap:6px; font-size:12px; color:#596575; }
.row-fields input { width:100%; min-width:0; height:38px; box-sizing:border-box; border:1px solid #cfd5df; border-radius:6px; padding:0 10px; background:#fff; color:#18222e; }
.server-field { margin-top:13px; }
.server-list { display:flex; flex-wrap:wrap; gap:8px 16px; }
.server-list label { display:flex; align-items:center; gap:6px; color:#263648; }
.form-actions { margin-top:6px; justify-content:flex-end; }
.submit-button { padding:9px 14px; border:0; border-radius:6px; background:#2869c5; color:#fff; cursor:pointer; }
button:disabled { opacity:.5; cursor:not-allowed; }
.batch-error { color:#c32f3e; font-size:13px; }
.batch-history { margin-top:18px; }
.history-group { padding:10px 0; border-top:1px solid #e7e9ee; }
.history-head { margin-bottom:8px; font-size:12px; font-weight:600; }
.history-actions { display:flex; gap:6px; }
.result-row { display:grid; grid-template-columns:minmax(130px,1fr) minmax(120px,1fr) 90px; gap:8px; align-items:center; padding:5px 0; font-size:12px; }
.result-row code { overflow-wrap:anywhere; }
.result-status { font-weight:600; }
.result-status--success { color:#198158; } .result-status--failed,.result-status--rolled_back { color:#c32f3e; }
.muted { color:#778292; overflow-wrap:anywhere; }
@media(max-width:700px) { .row-fields { grid-template-columns:1fr; } .result-row { grid-template-columns:1fr auto; } .result-row code,.result-row .muted { grid-column:1 / -1; } }
</style>
