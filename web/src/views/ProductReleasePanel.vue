<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useId } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { HttpError } from '../api/http'
import {
  advanceRelease, confirmRelease, getReleaseOptions, listProductReleases,
  previewRelease, reconcileRelease, type ReleaseApplication, type ReleaseBatch,
  type ReleasePlan, type ReleasePlanItem, type ReleaseSelection,
} from '../api/productReleases'
import { useIdentityRequestScope } from '../composables/useAuthorizedRequestScope'
import AppButton from '../components/ui/AppButton.vue'
import AppSelect from '../components/ui/AppSelect.vue'

const props = defineProps<{ productId: string; productName: string }>()
const { t, te, locale } = useI18n()
const dateFormat = computed(() => new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'short' }))
const titleId = useId()
const historyId = useId()
const dialog = ref<HTMLDialogElement>()
const heading = ref<HTMLElement>()
const availableApps = ref<ReleaseApplication[]>([])
const knownApps = ref<Record<string, ReleaseApplication>>({})
const optionsPage = ref(1)
const hasMoreOptions = ref(false)
const batches = ref<ReleaseBatch[]>([])
const selectedBatchID = ref('')
const chosen = ref<Record<string, ReleaseSelection>>({})
const preview = ref<ReleasePlan>()
const requestId = ref('')
const historyBusy = ref(false)
const historyPage = ref(1)
const hasMoreHistory = ref(false)
const optionsBusy = ref(false)
const dialogBusy = ref(false)
const writingBatch = ref('')
let historySequence = 0
const historyError = ref('')
const dialogError = ref('')
const scope = useIdentityRequestScope(() => {
  dialog.value?.close()
  availableApps.value = []
  knownApps.value = {}
  batches.value = []
  chosen.value = {}
  selectedBatchID.value = ''
  preview.value = undefined
}, () => true, undefined, () => false)

const selected = computed(() => Object.values(chosen.value))
const canPreview = computed(() => selected.value.length > 0 && selected.value.length <= 50
  && selected.value.every(input => input.environment && (!input.commitSha || /^[0-9a-f]{40}$/.test(input.commitSha))))
const selectedBatch = computed(() => batches.value.find(batch => batch.id === selectedBatchID.value))

function releaseError(error: unknown): string {
  if (error instanceof HttpError) {
    if (error.status === 403) return 'denied'
    if (error.status === 409) return 'changed'
    if (error.status === 400 || error.status === 422) return 'unsupported'
    if (error.status === 503) return 'unavailable'
  }
  return 'requestFailed'
}

function stateLabel(state: string): string {
  const key = 'workspace.release.state.' + state
  return t(te(key) ? key : 'workspace.release.state.unknown')
}

function eligible(app: ReleaseApplication): boolean {
  return app.canRelease !== false && app.environments.length > 0
}

function toggle(app: ReleaseApplication): void {
  if (!eligible(app) || dialogBusy.value) return
  if (chosen.value[app.applicationId]) delete chosen.value[app.applicationId]
  else if (selected.value.length < 50) {
    chosen.value[app.applicationId] = {
      applicationId: app.applicationId,
      branch: app.defaultBranch,
      environment: app.environments.length === 1 ? app.environments[0].name : '',
    }
  }
}

function appLabel(id: string): string {
  return knownApps.value[id]?.name ?? id
}

function targetLabels(item: ReleasePlanItem): string {
  const env = knownApps.value[item.applicationId]?.environments.find(entry => entry.name === item.environment)
  return item.targetIds.map(id => env?.targets?.find(target => target.id === id)?.name ?? id).join(' · ')
}

async function loadHistory(page = historyPage.value): Promise<void> {
  if (historyBusy.value || !scope.active()) return
  historyBusy.value = true
  const sequence = ++historySequence
  historyError.value = ''
  try {
    const result = await scope.run(() => listProductReleases(props.productId, page, scope.requestOptions))
    if (sequence === historySequence) {
      batches.value = result.items
      historyPage.value = page
      hasMoreHistory.value = result.hasMore ?? false
    }
  } catch (error) {
    if (scope.active()) {
      if (error instanceof HttpError && error.status === 403) {
        batches.value = []
        selectedBatchID.value = ''
      }
      historyError.value = releaseError(error)
    }
  } finally {
    historyBusy.value = false
  }
}

async function loadOptions(page = 1): Promise<void> {
  if (optionsBusy.value || !scope.active()) return
  optionsBusy.value = true
  try {
    const result = await scope.run(() => getReleaseOptions(props.productId, page, scope.requestOptions))
    availableApps.value = result.applications
    optionsPage.value = page
    hasMoreOptions.value = result.hasMore ?? false
    for (const app of result.applications) {
      knownApps.value[app.applicationId] = app
      if (!eligible(app)) delete chosen.value[app.applicationId]
    }
  } catch (error) {
    if (scope.active()) {
      if (error instanceof HttpError && error.status === 403) {
        availableApps.value = []
        knownApps.value = {}
        chosen.value = {}
      }
      if (dialog.value?.open) dialogError.value = releaseError(error)
    }
  } finally {
    optionsBusy.value = false
  }
}

function open(): void {
  if (!scope.active() || !dialog.value || dialog.value.open || dialogBusy.value) return
  chosen.value = {}
  preview.value = undefined
  dialogError.value = ''
  requestId.value = ''
  dialog.value.showModal()
  void loadOptions()
}

function cancelIfWriting(event: Event): void {
  if (dialogBusy.value) event.preventDefault()
}

function clearPreview(): void {
  preview.value = undefined
  requestId.value = ''
  dialogError.value = ''
  void nextTick(() => heading.value?.focus())
}

async function createPreview(): Promise<void> {
  if (!canPreview.value || dialogBusy.value) return
  dialogBusy.value = true
  dialogError.value = ''
  try {
    const inputs = selected.value.map(input => ({
      applicationId: input.applicationId,
      environment: input.environment,
      branch: input.branch?.trim() || undefined,
      commitSha: input.commitSha || undefined,
    }))
    preview.value = await scope.run(() => previewRelease(props.productId, inputs, scope.requestOptions))
    if (!dialog.value?.open) return
    requestId.value = preview.value.items[0]?.snapshotRef ?? ''
    await nextTick()
    heading.value?.focus()
  } catch (error) {
    if (scope.active() && dialog.value?.open) dialogError.value = releaseError(error)
  } finally {
    dialogBusy.value = false
  }
}

function remember(batch: ReleaseBatch): void {
  const existing = batches.value.find(item => item.id === batch.id)
  if (existing && existing.revision > batch.revision) return
  historySequence++
  batches.value = [batch, ...batches.value.filter(item => item.id !== batch.id)]
    .sort((a, b) => b.createdAt - a.createdAt)
  selectedBatchID.value = batch.id
}

async function startRelease(): Promise<void> {
  const plan = preview.value
  if (!plan || !requestId.value || dialogBusy.value) return
  dialogBusy.value = true
  dialogError.value = ''
  try {
    const batch = await scope.run(() => confirmRelease(requestId.value, plan, scope.requestOptions))
    if (historyPage.value !== 1) {
      historyPage.value = 1
      await loadHistory(1)
    }
    remember(batch)
    dialog.value?.close()
    if (batch.revision === 1) await update(batch, 'advance')
  } catch (error) {
    if (scope.active() && dialog.value?.open) dialogError.value = releaseError(error)
  } finally {
    dialogBusy.value = false
  }
}

async function update(batch: ReleaseBatch, action: 'advance' | 'reconcile'): Promise<void> {
  if (writingBatch.value || !scope.active()) return
  if (action === 'advance' && batch.state !== 'pending') return
  writingBatch.value = batch.id
  historyError.value = ''
  try {
    const request = action === 'advance' ? advanceRelease : reconcileRelease
    remember(await scope.run(() => request(batch.id, batch.revision, scope.requestOptions)))
  } catch (error) {
    if (scope.active()) {
      const errorKey = releaseError(error)
      await loadHistory()
      historyError.value = errorKey
    }
  } finally {
    writingBatch.value = ''
  }
}

onMounted(() => {
  void loadHistory()
  void loadOptions()
})
defineExpose({ open })
</script>

<template>
  <section class="product-release" :aria-labelledby="historyId">
    <header class="product-release__header">
      <h2 :id="historyId">{{ t('workspace.release.history') }}</h2>
      <AppButton variant="ghost" :loading="historyBusy" :disabled="!!writingBatch" @click="loadHistory()">{{ t('workspace.refresh') }}</AppButton>
    </header>
    <p v-if="historyError" class="product-release__error" role="alert">{{ t('workspace.release.' + historyError) }}</p>
    <ul v-if="batches.length" class="product-release__items">
      <li v-for="batch in batches" :key="batch.id">
        <button class="product-release__record" type="button" :aria-expanded="selectedBatchID === batch.id"
          @click="selectedBatchID = selectedBatchID === batch.id ? '' : batch.id">
          <span>{{ dateFormat.format(batch.createdAt) }} · {{ t('workspace.release.appCount', { n: batch.items.length }, batch.items.length) }}</span>
          <span class="product-release__state" :data-state="batch.state">{{ stateLabel(batch.state) }}</span>
        </button>
        <div v-if="selectedBatch === batch" class="product-release__details">
          <ol class="product-release__items">
            <li v-for="(item, index) in batch.items" :key="item.applicationId" class="product-release__row">
              <div>
                <strong>{{ appLabel(item.applicationId) }}</strong>
                <p class="product-release__meta">{{ batch.plan.items[index]?.environment }} · <code :title="batch.plan.items[index]?.commitSha">{{ batch.plan.items[index]?.commitSha.slice(0, 12) }}</code></p>
              </div>
              <div>
                <span class="product-release__state" :data-state="item.state">{{ stateLabel(item.state) }}</span>
                <p v-if="item.runId" class="product-release__meta">
                  <RouterLink :to="{ name: 'run-detail', params: { id: item.runId } }">{{ t('workspace.openRun') }}</RouterLink>
                </p>
              </div>
            </li>
          </ol>
          <p v-if="batch.state === 'needs_reconciliation'" class="product-release__meta">{{ t('workspace.release.unknownHint') }}</p>
          <p v-if="batch.items.some(item => ['frozen_plan_changed', 'admission_rejected'].includes(item.reason ?? ''))"
            class="product-release__meta">{{ t('workspace.release.changed') }}</p>
          <div class="product-release__actions">
            <AppButton v-if="batch.state === 'pending'" :loading="writingBatch === batch.id" :disabled="historyBusy || !!writingBatch && writingBatch !== batch.id" variant="primary"
              @click="update(batch, 'advance')">{{ t('workspace.release.nextApplication') }}</AppButton>
            <AppButton v-else-if="['running', 'waiting_approval', 'needs_reconciliation'].includes(batch.state)"
              :loading="writingBatch === batch.id" :disabled="historyBusy || !!writingBatch && writingBatch !== batch.id" @click="update(batch, 'reconcile')">{{ t('workspace.release.checkResult') }}</AppButton>
          </div>
        </div>
      </li>
    </ul>
    <p v-else-if="!historyError" class="product-release__meta" role="status">{{ t(historyBusy ? 'workspace.release.loading' : 'workspace.release.emptyHistory') }}</p>
    <nav v-if="historyPage > 1 || hasMoreHistory" class="product-release__actions"
      :aria-label="t('workspace.page', { page: historyPage })">
      <AppButton variant="ghost" :disabled="historyBusy || !!writingBatch || historyPage === 1"
        @click="loadHistory(historyPage - 1)">{{ t('workspace.previous') }}</AppButton>
      <span class="product-release__meta">{{ t('workspace.page', { page: historyPage }) }}</span>
      <AppButton variant="ghost" :disabled="historyBusy || !!writingBatch || !hasMoreHistory"
        @click="loadHistory(historyPage + 1)">{{ t('workspace.next') }}</AppButton>
    </nav>
  </section>

  <dialog ref="dialog" class="product-release__dialog" :aria-labelledby="titleId" @cancel="cancelIfWriting">
    <form class="product-release__form" @submit.prevent="preview ? startRelease() : createPreview()">
      <h2 :id="titleId" ref="heading" tabindex="-1">{{ t(preview ? 'workspace.release.confirmTitle' : 'workspace.release.prepareTitle') }}</h2>
      <p class="product-release__meta">{{ productName }} · {{ t(preview ? 'workspace.release.confirmHint' : 'workspace.release.prepareHint') }}</p>
      <p v-if="dialogError" class="product-release__error" role="alert">{{ t('workspace.release.' + dialogError) }}</p>
      <ol v-if="preview" class="product-release__items">
        <li v-for="item in preview.items" :key="item.applicationId" class="product-release__plan">
          <strong>{{ appLabel(item.applicationId) }}</strong>
          <dl>
            <div><dt>{{ t('workspace.release.commit') }}</dt><dd><code>{{ item.commitSha }}</code></dd></div>
            <div><dt>{{ t('workspace.release.environment') }}</dt><dd>{{ item.environment }}</dd></div>
            <div><dt>{{ t('workspace.release.targets') }}</dt><dd>{{ targetLabels(item) }}</dd></div>
          </dl>
        </li>
      </ol>
      <p v-else-if="optionsBusy" class="product-release__meta" role="status">{{ t('workspace.release.loading') }}</p>
      <ul v-else-if="availableApps.length" class="product-release__items">
        <li v-for="app in availableApps" :key="app.applicationId" class="product-release__plan">
          <label class="product-release__choice">
            <input type="checkbox" :checked="!!chosen[app.applicationId]"
              :disabled="dialogBusy || !eligible(app) || (!chosen[app.applicationId] && selected.length >= 50)"
              @change="toggle(app)">
            <strong>{{ app.name }}</strong>
          </label>
          <p v-if="!eligible(app)" class="product-release__meta">{{ t('workspace.release.ineligible') }}</p>
          <div v-else-if="chosen[app.applicationId]" class="product-release__fields">
            <label>{{ t('workspace.release.branch') }}
              <input v-model="chosen[app.applicationId].branch" class="product-release__input" :placeholder="app.defaultBranch" :disabled="dialogBusy" maxlength="256">
            </label>
            <div>
              <label :for="titleId + '-' + app.applicationId">{{ t('workspace.release.environment') }}</label>
              <AppSelect :input-id="titleId + '-' + app.applicationId" v-model="chosen[app.applicationId].environment"
                min-width="0" portal :disabled="dialogBusy" :placeholder="t('workspace.release.chooseEnvironment')"
                :options="app.environments.map(env => ({ value: env.name, label: env.name }))" />
            </div>
            <label class="product-release__commit">{{ t('workspace.release.optionalCommit') }}
              <input v-model.trim="chosen[app.applicationId].commitSha" class="product-release__input mono"
                spellcheck="false" :disabled="dialogBusy" maxlength="40" pattern="[0-9a-f]{40}" :placeholder="t('workspace.release.branchHead')">
            </label>
          </div>
        </li>
      </ul>
      <p v-else-if="!dialogError" class="product-release__meta">{{ t('workspace.release.noOptions') }}</p>
      <nav v-if="!preview && (optionsPage > 1 || hasMoreOptions)" class="product-release__actions"
        :aria-label="t('workspace.page', { page: optionsPage })">
        <AppButton variant="ghost" :disabled="optionsBusy || dialogBusy || optionsPage === 1"
          @click="loadOptions(optionsPage - 1)">{{ t('workspace.previous') }}</AppButton>
        <span class="product-release__meta">{{ t('workspace.page', { page: optionsPage }) }}</span>
        <AppButton variant="ghost" :disabled="optionsBusy || dialogBusy || !hasMoreOptions"
          @click="loadOptions(optionsPage + 1)">{{ t('workspace.next') }}</AppButton>
      </nav>
      <p v-if="!preview && selected.length" class="product-release__meta" role="status">
        {{ t('workspace.release.appCount', { n: selected.length }, selected.length) }}
      </p>
      <footer class="product-release__actions">
        <AppButton :disabled="dialogBusy" @click="dialog?.close()">{{ t('workspace.release.cancel') }}</AppButton>
        <AppButton v-if="preview" :disabled="dialogBusy" @click="clearPreview">{{ t('workspace.release.back') }}</AppButton>
        <AppButton :key="preview ? 'confirm' : 'preview'" type="submit" variant="primary" :loading="dialogBusy" :disabled="preview ? !requestId : !canPreview">
          {{ t(preview ? 'workspace.release.start' : 'workspace.release.preview') }}
        </AppButton>
      </footer>
    </form>
  </dialog>
</template>

<style scoped>
.product-release,
.product-release__dialog {
  background: var(--color-card);
  color: var(--color-text);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
}
.product-release { padding: 14px; }
.product-release__header,
.product-release__actions,
.product-release__row,
.product-release__record {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.product-release__header,
.product-release__row,
.product-release__record { justify-content: space-between; }
.product-release__actions { justify-content: flex-end; }
.product-release h2,
.product-release__dialog h2 { font-size: 1rem; margin: 0; }
.product-release__items { display: grid; gap: 5px; padding: 0; margin: 0; list-style: none; }
.product-release__record {
  width: 100%;
  font: inherit;
  color: var(--color-text);
  border: 0;
  border-radius: var(--rounded);
  background: var(--color-inset);
  text-align: start;
  padding: 7px 10px;
  cursor: pointer;
  min-height: 44px;
}
.product-release__record:hover { background: var(--color-card-2); }
.product-release__record:focus-visible { outline: 2px solid var(--color-primary); outline-offset: -2px; }
.product-release__details { padding: 10px; }
.product-release__row { padding: 6px 0; border-block-end: 1px solid var(--color-border); }
.product-release__row:last-child { border-block-end: 0; }
.product-release__meta { color: var(--color-dim); font-size: .875rem; margin: 0; }
.product-release__error { color: var(--color-red); margin: 0; }
.product-release__state { color: var(--color-dim); font-size: .875rem; }
.product-release__state[data-state="succeeded"] { color: var(--color-green); }
.product-release__state:is([data-state="failed"], [data-state="blocked"]) { color: var(--color-red); }
.product-release__dialog {
  position: fixed;
  inset: 0;
  margin: auto;
  width: min(580px, calc(100% - 24px));
  max-block-size: calc(100vh - 24px);
  max-block-size: calc(100dvh - 24px);
  padding: 16px;
  overflow: auto;
  box-shadow: var(--shadow-modal);
}
.product-release__dialog:modal { overscroll-behavior: contain; }
.product-release__dialog::backdrop { background: rgb(10 16 28 / 58%); overflow: hidden; overscroll-behavior: contain; }
.product-release__form { display: grid; gap: 12px; overflow-wrap: anywhere; }
.product-release__plan { min-width: 0; padding: 10px; border: 1px solid var(--color-border); border-radius: var(--rounded); }
.product-release__choice { display: flex; align-items: center; gap: 8px; min-height: 32px; }
.product-release__choice input { accent-color: var(--color-primary); }
.product-release__fields,
.product-release__plan dl { display: grid; gap: 8px; margin: 8px 0 0; }
.product-release__fields > * { display: grid; gap: 4px; min-width: 0; }
.product-release__plan dl { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.product-release__plan dl > div { display: grid; gap: 2px; min-width: 0; }
.product-release__plan dl > div:first-child { grid-column: 1 / -1; }
.product-release__plan dt { color: var(--color-dim); font-size: .875rem; }
.product-release__plan dd { margin: 0; overflow-wrap: anywhere; }
.product-release__plan dd code { font-size: clamp(.6875rem, 2.3vw, .8125rem); }
.product-release__input {
  width: 100%;
  min-width: 0;
  min-height: 38px;
  padding: 7px 10px;
  color: var(--color-text);
  background: var(--color-inset);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  font: inherit;
}
.product-release__input:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 1px; }
@media (min-width: 540px) {
  .product-release__fields { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); }
  .product-release__commit { grid-column: 1 / -1; }
}
@media (max-width: 600px), (pointer: coarse) {
  .product-release__choice, .product-release__input, .product-release__actions :deep(button) { min-height: 44px; }
  .product-release__input { font-size: 16px; }
}
</style>
