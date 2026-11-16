<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { type ArtifactDTO, type DeployBatch, deployBatch, listDeployBatches, retryDeployBatch, continueDeployBatch } from '../../api/runs'
import { listServers, type Server } from '../../api/servers'
import { HttpError } from '../../api/http'
import AppSelect from '../ui/AppSelect.vue'
import { usePermission } from '../../composables/usePermission'
import { useIdentityRequestScope } from '../../composables/useAuthorizedRequestScope'
import { useSessionStore } from '../../stores/session'

const props = defineProps<{ runId: string; artifacts: ArtifactDTO[]; canCreate: boolean; projectId?: string; environmentId?: string }>()
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
const session = useSessionStore()
const readPermission = usePermission(() => ({ action: 'run.read', resourceId: props.runId }))
const deployPermission = usePermission(() => ({ action: 'deployment.execute', resourceId: props.environmentId ?? '', environmentId: props.environmentId ?? '' }))
const editPermission = usePermission(() => ({ action: 'pipeline.edit', resourceId: props.projectId ?? '' }))
const canDeploy = computed(() => readPermission.allowed.value && deployPermission.allowed.value && (session.user?.multiTenant !== true || !!props.environmentId))
const canEdit = computed(() => canDeploy.value && editPermission.allowed.value && props.canCreate && (session.user?.multiTenant !== true || !!props.projectId))
let generation = 0
let refreshSequence = 0
let requestController = new AbortController()
const scope = useIdentityRequestScope(resetContext)
function resetContext() {
  generation++
  refreshSequence++
  requestController.abort()
  requestController = new AbortController()
  clearInterval(poll)
  poll = undefined
  rows.value = []
  servers.value = []
  batches.value = []
  idempotencyKey.value = ''
  error.value = ''
  busy.value = false
}
function requestOptions() { return { ...scope.requestOptions, signal: requestController.signal } }
function current(ticket: number) { return ticket === generation && scope.active() && readPermission.allowed.value }


const fileArtifacts = computed(() => props.artifacts.filter((a) => a.type !== 'image'))
const artifactOptions = computed(() => fileArtifacts.value.map((a) => ({
  value: a.id,
  label: `${a.name} · ${a.type} · ${typeof a.metadata.workspacePath === 'string' ? a.metadata.workspacePath : a.reference.slice(0, 12)}`,
})))

function addRow(): void {
  if (!scope.active() || !canEdit.value) return
  rows.value.push({ key: crypto.randomUUID(), artifactId: fileArtifacts.value[0]?.id ?? '', serverIds: [], path: '' })
}

function removeRow(key: string): void {
  if (!scope.active() || !canEdit.value || busy.value) return
  const index = rows.value.findIndex((row) => row.key === key)
  if (index !== -1) rows.value.splice(index, 1)
}

function toggleServer(row: DraftItem, id: string): void {
  if (!scope.active() || !canEdit.value || busy.value) return
  row.serverIds = row.serverIds.includes(id) ? row.serverIds.filter((value) => value !== id) : [...row.serverIds, id]
}

async function refresh(): Promise<void> {
  if (!scope.active() || !readPermission.allowed.value) return
  const ticket = generation, sequence = ++refreshSequence, id = props.runId
  const response = await scope.run(() => listDeployBatches(id, requestOptions()))
  if (current(ticket) && sequence === refreshSequence) batches.value = response.batches
}

async function load(): Promise<void> {
  if (!scope.active() || !readPermission.allowed.value) return
  const ticket = generation
  try {
    await refresh()
    if (!current(ticket) || !canEdit.value) return
    const available = await scope.run(() => listServers(requestOptions()))
    if (current(ticket) && canEdit.value) servers.value = available
  } catch (cause) {
    if (current(ticket)) error.value = message(cause)
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
  if (!scope.active() || !readPermission.allowed.value || !canEdit.value || busy.value || !validate()) return
  const ticket = generation, id = props.runId
  busy.value = true
  error.value = ''
  if (!idempotencyKey.value) idempotencyKey.value = crypto.randomUUID()
  try {
    await scope.run(() => deployBatch(id, {
      idempotencyKey: idempotencyKey.value,
      items: rows.value.map((row) => ({
        artifactId: row.artifactId,
        serverIds: [...row.serverIds],
        deployConfig: { path: row.path.trim() },
      })),
    }, requestOptions()))
    if (!current(ticket) || !canEdit.value) return
    idempotencyKey.value = ''
    rows.value = []
    await refresh()
    if (current(ticket) && canDeploy.value) emit('completed')
  } catch (cause) {
    if (!current(ticket)) return
    error.value = message(cause)
    await refresh().catch(() => undefined)
  } finally {
    if (current(ticket)) busy.value = false
  }
}

async function resume(batch: DeployBatch, mode: 'retry' | 'continue'): Promise<void> {
  if (!scope.active() || !readPermission.allowed.value || !canDeploy.value || busy.value) return
  const ticket = generation, id = props.runId
  busy.value = true
  error.value = ''
  try {
    if (mode === 'retry') await scope.run(() => retryDeployBatch(id, batch.id, requestOptions()))
    else await scope.run(() => continueDeployBatch(id, batch.id, requestOptions()))
    if (!current(ticket) || !canDeploy.value) return
    await refresh()
    if (current(ticket) && canDeploy.value) emit('completed')
  } catch (cause) {
    if (!current(ticket)) return
    error.value = message(cause)
    await refresh().catch(() => undefined)
  } finally {
    if (current(ticket)) busy.value = false
  }
}

watch(rows, () => { idempotencyKey.value = '' }, { deep: true })
watch(() => props.runId, () => { resetContext(); start() }, { flush: 'sync' })
watch(readPermission.allowed, allowed => { if (!allowed) resetContext(); else start() }, { flush: 'sync' })
watch(canEdit, allowed => { if (!allowed) { rows.value = []; servers.value = []; idempotencyKey.value = '' } else void load() }, { flush: 'sync' })
watch(canDeploy, allowed => { if (!allowed) { generation++; busy.value = false; error.value = '' } }, { flush: 'sync' })
function start() {
  if (!scope.active() || !readPermission.allowed.value) return
  void load()
  clearInterval(poll)
  poll = setInterval(() => { if (!busy.value) void refresh().catch(() => undefined) }, 5000)
}
onMounted(start)
onUnmounted(resetContext)
</script>

<template>
  <section class="batch-deploy" :aria-label="t('batchDeploy.title')">
    <div class="batch-head">
      <h2>{{ t('batchDeploy.title') }}</h2>
      <button v-if="canEdit && fileArtifacts.length" type="button" class="batch-button" :disabled="busy" @click="addRow">+ {{ t('batchDeploy.add') }}</button>
    </div>
    <div v-if="canEdit && rows.length" class="batch-form">
      <div v-for="(row, index) in rows" :key="row.key" class="batch-row">
        <div class="row-head">
          <strong>{{ t('batchDeploy.item', { n: index + 1 }) }}</strong>
          <button type="button" class="remove-button" :aria-label="t('batchDeploy.remove')" :disabled="busy" @click="removeRow(row.key)">×</button>
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
            <button v-if="canDeploy && batch.items.some((item) => item.status === 'failed' || item.status === 'rolled_back')" type="button" :disabled="busy" @click="resume(batch, 'retry')">{{ t('batchDeploy.retry') }}</button>
            <button v-if="canDeploy && batch.items.some((item) => item.status === 'pending')" type="button" :disabled="busy" @click="resume(batch, 'continue')">{{ t('batchDeploy.continue') }}</button>
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
.batch-deploy {
  margin-top: 24px;
  padding-top: 22px;
  border-top: 1px solid var(--color-border);
}
.batch-head,
.row-head,
.history-head,
.form-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
h2 {
  margin: 0;
  font-size: 17px;
}
h3 {
  margin: 0 0 12px;
  font-size: 14px;
}
.batch-button,
.history-actions button {
  padding: 7px 11px;
  border: 1px solid var(--color-border-strong);
  border-radius: 6px;
  background: var(--color-card);
  color: var(--color-primary);
  cursor: pointer;
}
.batch-form {
  margin-top: 16px;
}
.batch-row {
  padding: 15px 0;
  border-top: 1px solid var(--color-border);
}
.row-head {
  margin-bottom: 12px;
  font-size: 13px;
}
.remove-button {
  width: 30px;
  height: 30px;
  border: 0;
  background: transparent;
  color: var(--color-dim);
  font-size: 20px;
  cursor: pointer;
}
.row-fields {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 14px;
}
.row-fields label,
.server-field {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
  color: var(--color-dim);
}
.row-fields input {
  width: 100%;
  min-width: 0;
  height: 38px;
  box-sizing: border-box;
  border: 1px solid var(--color-border-strong);
  border-radius: 6px;
  padding: 0 10px;
  background: var(--color-card);
  color: var(--color-text);
}
.server-field {
  margin-top: 13px;
}
.server-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
}
.server-list label {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--color-text);
}
.form-actions {
  margin-top: 6px;
  justify-content: flex-end;
}
.submit-button {
  padding: 9px 14px;
  border: 0;
  border-radius: 6px;
  background: var(--color-primary);
  color: var(--color-on-primary, #fff);
  cursor: pointer;
}
button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.batch-error {
  color: var(--color-red);
  font-size: 13px;
}
.batch-history {
  margin-top: 18px;
}
.history-group {
  padding: 10px 0;
  border-top: 1px solid var(--color-border);
}
.history-head {
  margin-bottom: 8px;
  font-size: 12px;
  font-weight: 600;
}
.history-actions {
  display: flex;
  gap: 6px;
}
.result-row {
  display: grid;
  grid-template-columns: minmax(130px, 1fr) minmax(120px, 1fr) 90px;
  gap: 8px;
  align-items: center;
  padding: 5px 0;
  font-size: 12px;
}
.result-row code {
  overflow-wrap: anywhere;
}
.result-status {
  font-weight: 600;
}
.result-status--success {
  color: var(--color-green);
}
.result-status--failed,
.result-status--rolled_back {
  color: var(--color-red);
}
.muted {
  color: var(--color-dim);
  overflow-wrap: anywhere;
}
@media (max-width: 700px) {
  .row-fields {
    grid-template-columns: 1fr;
  }
  .result-row {
    grid-template-columns: 1fr auto;
  }
  .result-row code,
  .result-row .muted {
    grid-column: 1 / -1;
  }
}
button {
  min-height: 36px;
  font: inherit;
  font-size: 13px;
}
button:focus-visible,
input:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}
.server-list label {
  min-height: 36px;
}
.history-actions {
  flex-wrap: wrap;
}
@media (max-width: 639px) {
  button,
  .server-list label {
    min-height: 44px;
  }
  .remove-button {
    width: 44px;
  }
  .row-fields input {
    height: 44px;
    font-size: 16px;
  }
}
</style>
