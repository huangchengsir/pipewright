<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import AppButton from '../../components/ui/AppButton.vue'
import { useConfirm } from '../../composables/useConfirm'
import { HttpError } from '../../api/http'
import FormField from '../../components/ui/FormField.vue'
import {
  databaseStatus,
  databasePreflight,
  databaseTestConnection,
  databaseAction,
  recoveryRequest,
  RecoveryError,
  type MigrationState,
} from '../../api/databaseMigration'
import '../../styles/settings-surface.css'
const props = defineProps<{ recovery?: boolean }>()
const { t, te } = useI18n()
const router = useRouter()
const confirm = useConfirm()
const state = ref<MigrationState>()
const driver = ref('postgres')
const dsn = ref('')
const managed = ref(false)
const host = ref('')
const port = ref(5432)
const database = ref('')
const schema = ref('')
const dbUser = ref('')
const dbPassword = ref('')
const file = ref('')
const advanced = ref(false)
const busy = ref(false)
const targetForm = ref<HTMLFormElement>()
const probeBusy = ref(false)
const probeKind = ref<'connection' | 'path'>()
const probeErrorCode = ref('')
const probeError = computed(() => {
  const key = `databaseMigration.errors.${probeErrorCode.value}`
  return te(key) ? t(key) : t('databaseMigration.failure')
})
let probeController: AbortController | undefined
let probeTimer: ReturnType<typeof setTimeout> | undefined
let probeGeneration = 0
function clearProbe() {
  probeGeneration++
  probeController?.abort()
  probeController = undefined
  clearTimeout(probeTimer)
  probeBusy.value = false
  probeKind.value = undefined
  probeErrorCode.value = ''
}
const errorCode = ref('')
const error = computed(() => {
  if (!errorCode.value) return ''
  const key = `databaseMigration.errors.${errorCode.value}`
  return te(key) ? t(key) : t('databaseMigration.failure')
})
const needsLogin = ref(false)
const username = ref('')
const password = ref('')
const csrf = ref('')
const approval = ref<{ input: string; taskId: string; revision: number }>()
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
const requestController = new AbortController()
const fields = () =>
  advanced.value
    ? { driver: driver.value, dsn: dsn.value }
    : driver.value === 'sqlite'
      ? { driver: driver.value, file: file.value }
      : {
          driver: driver.value,
          host: host.value,
          port: port.value,
          database: database.value,
          schema: schema.value,
          username: dbUser.value,
          password: dbPassword.value,
        }
const inputStamp = () => JSON.stringify([fields(), managed.value])
const editable = computed(
  () =>
    !state.value?.phase ||
    ['prepared', 'aborted', 'completed'].includes(state.value.phase),
)
const engines = computed(() =>
  [
    { value: 'sqlite', label: 'SQLite' },
    { value: 'mysql', label: 'MySQL 8' },
    { value: 'postgres', label: 'PostgreSQL' },
    { value: 'sqlserver', label: 'SQL Server 2019 / 2022 / 2025' },
    { value: 'dm', label: 'DM8' },
  ].map((v) => ({ ...v, disabled: v.value === state.value?.current })),
)
const ready = computed(
  () =>
    state.value?.phase === 'prepared' &&
    approval.value?.input === inputStamp() &&
    approval.value?.taskId === state.value.taskId &&
    approval.value?.revision === state.value.revision,
)
watch(driver, (value) => {
  port.value =
    (
      { mysql: 3306, postgres: 5432, sqlserver: 1433, dm: 5236 } as Record<
        string,
        number
      >
    )[value] ?? 0
})
watch(
  [
    driver,
    dsn,
    managed,
    host,
    port,
    database,
    schema,
    dbUser,
    dbPassword,
    file,
    advanced,
  ],
  () => {
    approval.value = undefined
    errorCode.value = ''
    clearProbe()
  },
  { flush: 'sync' },
)
watch(
  () => [state.value?.taskId, state.value?.revision],
  () => {
    if (
      approval.value &&
      (approval.value.taskId !== state.value?.taskId ||
        approval.value.revision !== state.value?.revision)
    )
      approval.value = undefined
  },
  { flush: 'sync' },
)
watch(
  editable,
  (value) => {
    if (!value) clearProbe()
  },
  { flush: 'sync' },
)
async function testConnection() {
  if (
    busy.value ||
    probeBusy.value ||
    !editable.value ||
    props.recovery ||
    !targetForm.value?.reportValidity()
  )
    return
  clearProbe()
  const generation = probeGeneration
  const stamp = inputStamp()
  const controller = new AbortController()
  probeController = controller
  probeBusy.value = true
  probeTimer = setTimeout(() => {
    if (generation !== probeGeneration || disposed) return
    controller.abort()
    probeGeneration++
    probeBusy.value = false
    probeErrorCode.value = 'database_connection_timeout'
  }, 10000)
  try {
    const result = await databaseTestConnection(fields(), controller.signal)
    if (
      disposed ||
      generation !== probeGeneration ||
      stamp !== inputStamp() ||
      !editable.value
    )
      return
    probeKind.value = result.kind
  } catch (e) {
    if (disposed || generation !== probeGeneration || controller.signal.aborted)
      return
    probeErrorCode.value =
      e instanceof HttpError ? (e.apiError?.code ?? 'unknown') : 'unknown'
  } finally {
    if (generation === probeGeneration) {
      clearTimeout(probeTimer)
      probeBusy.value = false
      probeController = undefined
    }
  }
}
function showError(e: unknown) {
  if (disposed) return
  const code =
    e instanceof RecoveryError
      ? e.code
      : e instanceof HttpError
        ? e.apiError?.code
        : undefined
  errorCode.value = code ?? 'unknown'
}
const stage = computed(
  () =>
    (
      ({
        prepared: 0,
        quiescing: 1,
        frozen: 2,
        copying: 2,
        verified: 3,
        activation_intent: 3,
        awaiting_restart: 3,
        completed: 4,
        aborted: 0,
      }) as Record<string, number>
    )[state.value?.phase ?? 'prepared'] ?? 0,
)
async function load() {
  try {
    if (props.recovery) {
      const result = await recoveryRequest<{
        state: MigrationState
        csrf: string
      }>('session', undefined, undefined, requestController.signal)
      if (disposed) return
      state.value = result.state
      csrf.value = result.csrf
      needsLogin.value = false
    } else {
      const result = await databaseStatus(requestController.signal)
      if (disposed) return
      const initial = !state.value
      state.value = result
      if (initial && driver.value === result.current)
        driver.value =
          engines.value.find((item) => !item.disabled)?.value ?? 'sqlite'
    }
  } catch (e) {
    if (disposed) return
    if (props.recovery && e instanceof RecoveryError && e.status === 401) {
      const mode = await recoveryRequest<{ recovery: boolean }>(
        'mode',
        undefined,
        undefined,
        requestController.signal,
      ).catch(() => null)
      if (disposed) return
      if (mode && !mode.recovery) {
        void router.replace('/settings/database')
        return
      }
      needsLogin.value = true
    } else showError(e)
  } finally {
    if (!disposed)
      timer = setTimeout(() => {
        void load()
      }, 2500)
  }
}
async function login() {
  busy.value = true
  errorCode.value = ''
  try {
    const result = await recoveryRequest<{ csrf: string }>(
      'login',
      { username: username.value, password: password.value },
      undefined,
      requestController.signal,
    )
    if (disposed) return
    csrf.value = result.csrf
    needsLogin.value = false
    clearTimeout(timer)
    await load()
  } catch (e) {
    showError(e)
  } finally {
    password.value = ''
    busy.value = false
  }
}
async function preflight() {
  clearProbe()
  busy.value = true
  errorCode.value = ''
  const stamp = inputStamp()
  try {
    const result = await databasePreflight(
      fields(),
      managed.value,
      requestController.signal,
    )
    if (disposed) return
    state.value = result
    if (stamp === inputStamp() && result.phase === 'prepared' && result.taskId)
      approval.value = {
        input: stamp,
        taskId: result.taskId,
        revision: result.revision,
      }
  } catch (e) {
    showError(e)
  } finally {
    busy.value = false
  }
}
async function action(name: string) {
  clearProbe()
  if (!state.value || (name === 'start' && !ready.value)) return
  // Bind the confirmation to the exact preflight/task the operator saw.
  const request = { ...state.value }
  const confirmedInput = inputStamp()
  if (['start', 'activate', 'abort', 'reconcile'].includes(name)) {
    const ok = await confirm.open({
      title: t(`databaseMigration.confirmTitles.${name}`),
      body: `${request.current} → ${request.target ?? request.current}. ${t('databaseMigration.confirmBody')}`,
      confirmCheck: t('databaseMigration.confirm'),
      confirmLabel: t(
        `databaseMigration.${name === 'activate' ? 'activate' : name === 'abort' ? 'abort' : name === 'reconcile' ? 'reconcile' : 'start'}`,
      ),
      variant: 'primary',
    })
    if (!ok || disposed) return
  }
  if (
    state.value?.taskId !== request.taskId ||
    state.value?.revision !== request.revision ||
    (name === 'start' && (!ready.value || confirmedInput !== inputStamp()))
  ) {
    errorCode.value = 'database_state_changed'
    return
  }
  busy.value = true
  errorCode.value = ''
  try {
    if (props.recovery) {
      state.value = await recoveryRequest<MigrationState>(
        name,
        { taskId: request.taskId, revision: request.revision },
        csrf.value,
        requestController.signal,
      )
      if (name === 'abort') void router.replace('/settings/database')
    } else {
      await databaseAction(name, request, requestController.signal)
      if (name === 'start') {
        dsn.value = ''
        void router.push('/database-recovery')
      } else {
        approval.value = undefined
        clearTimeout(timer)
        await load()
      }
    }
  } catch (e) {
    showError(e)
    clearTimeout(timer)
    await load()
  } finally {
    busy.value = false
  }
}
onMounted(() => {
  void load()
})
onBeforeUnmount(() => {
  disposed = true
  clearProbe()
  clearTimeout(timer)
  requestController.abort()
  dsn.value = ''
  dbPassword.value = ''
  password.value = ''
  approval.value = undefined
  csrf.value = ''
})
</script>
<template>
  <section
    class="settings-page database-page"
    :class="{ 'database-recovery': recovery }"
  >
    <header>
      <h2>{{ t('databaseMigration.title') }}</h2>
      <p>{{ t('databaseMigration.subtitle') }}</p>
    </header>
    <p v-if="error" class="database-error" role="alert">{{ error }}</p>
    <form v-if="needsLogin" class="database-panel" @submit.prevent="login">
      <h3>{{ t('databaseMigration.recovery') }}</h3>
      <p>{{ t('databaseMigration.authRequired') }}</p>
      <FormField field-id="db-username" :label="t('databaseMigration.username')"
        ><input
          id="db-username"
          v-model="username"
          autocomplete="username"
          required
      /></FormField>
      <FormField field-id="db-password" :label="t('databaseMigration.password')"
        ><input
          id="db-password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          required
      /></FormField>
      <AppButton type="submit" variant="primary" :loading="busy">{{
        t('databaseMigration.login')
      }}</AppButton>
    </form>
    <template v-else>
      <div v-if="state" class="database-panel" aria-live="polite">
        <div class="database-summary">
          <span>{{ t('databaseMigration.current') }}</span
          ><strong>{{ state.current }}</strong
          ><span v-if="state.target">→ {{ state.target }}</span>
        </div>
        <dl v-if="state.info" class="database-metadata">
          <div>
            <dt>{{ t('databaseMigration.version') }}</dt>
            <dd>{{ state.info.version || '—' }}</dd>
          </div>
          <div>
            <dt>{{ t('databaseMigration.location') }}</dt>
            <dd>
              {{
                state.info.file ||
                [state.info.server, state.info.database, state.info.schema]
                  .filter(Boolean)
                  .join(' / ')
              }}
            </dd>
          </div>
          <div>
            <dt>{{ t('databaseMigration.identity') }}</dt>
            <dd>{{ state.info.identity }}</dd>
          </div>
        </dl>
        <ol
          v-if="state.phase && !['completed', 'aborted'].includes(state.phase)"
          class="database-stages"
        >
          <li
            v-for="(step, index) in ['check', 'drain', 'copy', 'restart']"
            :key="step"
            :class="{ done: index < stage, current: index === stage }"
            :aria-current="index === stage ? 'step' : undefined"
          >
            <span>{{ index < stage ? '✓' : index + 1 }}</span
            >{{ t(`databaseMigration.steps.${step}`) }}
          </li>
        </ol>
        <p v-if="state.phase">
          {{ t(`databaseMigration.phases.${state.phase}`) }}
        </p>
        <p v-if="state.phase === 'copying'">
          {{
            t('databaseMigration.tableProgress', {
              done: state.completedTables,
              total: state.totalTables,
            })
          }}
        </p>
        <p v-if="state.blockersUnavailable">
          {{ t('databaseMigration.blockersUnavailable') }}
        </p>
        <ul v-if="state.phase === 'quiescing' && state.blockers?.length">
          <li v-for="item in state.blockers" :key="item.kind">
            {{ t(`databaseMigration.blockers.${item.kind}`) }}: {{ item.count }}
          </li>
        </ul>
        <p v-if="state.phase === 'awaiting_restart' && !state.managed">
          {{ t('databaseMigration.environmentRestart') }}
        </p>
        <div class="database-actions" v-if="recovery">
          <AppButton
            v-if="
              state.recovery &&
              ['quiescing', 'frozen', 'copying'].includes(state.phase ?? '')
            "
            :loading="busy"
            variant="primary"
            @click="action('continue')"
            >{{ t('databaseMigration.continue') }}</AppButton
          >
          <AppButton
            v-if="
              ['activation_intent', 'awaiting_restart'].includes(
                state.phase ?? '',
              )
            "
            :loading="busy"
            variant="primary"
            @click="action('reconcile')"
            >{{ t('databaseMigration.reconcile') }}</AppButton
          >
          <AppButton
            v-if="state.phase === 'verified'"
            :loading="busy"
            variant="primary"
            @click="action('activate')"
            >{{ t('databaseMigration.activate') }}</AppButton
          >
          <AppButton
            v-if="
              state.phase && !['completed', 'aborted'].includes(state.phase)
            "
            :disabled="busy"
            @click="action('abort')"
            >{{ t('databaseMigration.abort') }}</AppButton
          >
        </div>
        <details v-if="state.report">
          <summary>
            {{ t('databaseMigration.details') }} ·
            {{ state.report.tables.length }}
          </summary>
          <div class="database-table">
            <table>
              <thead>
                <tr>
                  <th>{{ t('databaseMigration.table') }}</th>
                  <th>{{ t('databaseMigration.rows') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in state.report.tables" :key="item.table">
                  <td>{{ item.table }}</td>
                  <td>{{ item.rows }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </details>
      </div>
      <form
        v-if="!recovery && editable"
        ref="targetForm"
        class="database-panel"
        @submit.prevent="preflight"
      >
        <h3>{{ t('databaseMigration.target') }}</h3>
        <div
          class="database-engines"
          role="group"
          :aria-label="t('databaseMigration.engine')"
        >
          <AppButton
            v-for="engine in engines"
            :key="engine.value"
            :variant="driver === engine.value ? 'primary' : 'default'"
            :disabled="busy || engine.disabled"
            :aria-pressed="driver === engine.value"
            @click="driver = engine.value"
            >{{ engine.label }}</AppButton
          >
        </div>
        <div v-if="!advanced" class="database-fields">
          <FormField
            v-if="driver === 'sqlite'"
            field-id="db-file"
            :label="t('databaseMigration.sqliteFile')"
            ><input id="db-file" v-model="file" :disabled="busy" required
          /></FormField>
          <template v-else>
            <FormField field-id="db-host" :label="t('databaseMigration.host')"
              ><input
                id="db-host"
                v-model="host"
                :disabled="busy"
                autocomplete="off"
                required
            /></FormField>
            <FormField field-id="db-port" :label="t('databaseMigration.port')"
              ><input
                id="db-port"
                v-model.number="port"
                type="number"
                min="1"
                max="65535"
                :disabled="busy"
                required
            /></FormField>
            <FormField
              v-if="driver !== 'dm'"
              field-id="db-name"
              :label="t('databaseMigration.database')"
              ><input id="db-name" v-model="database" :disabled="busy" required
            /></FormField>
            <FormField
              v-if="['postgres', 'dm'].includes(driver)"
              field-id="db-schema"
              :label="t('databaseMigration.schema')"
              ><input id="db-schema" v-model="schema" :disabled="busy"
            /></FormField>
            <FormField
              field-id="db-user"
              :label="t('databaseMigration.username')"
              ><input
                id="db-user"
                v-model="dbUser"
                :disabled="busy"
                autocomplete="off"
                required
            /></FormField>
            <FormField
              field-id="db-secret"
              :label="t('databaseMigration.password')"
              ><input
                id="db-secret"
                v-model="dbPassword"
                type="password"
                :disabled="busy"
                autocomplete="new-password"
            /></FormField>
          </template>
        </div>
        <details
          class="database-advanced"
          @toggle="advanced = ($event.target as HTMLDetailsElement).open"
        >
          <summary>{{ t('databaseMigration.advanced') }}</summary>
          <FormField
            v-if="advanced"
            field-id="db-dsn"
            :label="t('databaseMigration.connection')"
            :hint="t('databaseMigration.connectionHint')"
            ><input
              id="db-dsn"
              v-model="dsn"
              type="password"
              autocomplete="off"
              :disabled="busy"
              required
          /></FormField>
        </details>
        <p v-if="driver === 'dm'">{{ t('databaseMigration.dmDriver') }}</p>
        <label class="database-check"
          ><input v-model="managed" type="checkbox" :disabled="busy" />{{
            t('databaseMigration.managed')
          }}</label
        >
        <p>
          {{
            managed
              ? t('databaseMigration.managedHint')
              : t('databaseMigration.environmentRestart')
          }}
        </p>
        <div class="database-actions">
          <AppButton
            type="button"
            data-testid="database-test-connection"
            :loading="probeBusy"
            :disabled="busy"
            @click="testConnection"
            >{{
              t(
                driver === 'sqlite'
                  ? 'databaseMigration.checkPath'
                  : 'databaseMigration.testConnection',
              )
            }}</AppButton
          >
          <AppButton type="submit" :loading="busy">{{
            t('databaseMigration.preflight')
          }}</AppButton
          ><AppButton
            v-if="state?.phase === 'prepared'"
            :disabled="busy"
            @click="action('abort')"
            >{{ t('databaseMigration.discard') }}</AppButton
          >
        </div>
        <div class="database-probe-feedback" role="status" aria-live="polite">
          <span v-if="probeBusy">{{
            t('databaseMigration.testingConnection')
          }}</span>
          <span v-else-if="probeKind" class="database-probe-success">{{
            t(
              probeKind === 'path'
                ? 'databaseMigration.pathChecked'
                : 'databaseMigration.connectionSucceeded',
            )
          }}</span>
          <span v-else-if="probeErrorCode" class="database-probe-error">{{
            probeError
          }}</span>
          <span class="database-probe-hint">{{
            t(
              driver === 'sqlite'
                ? 'databaseMigration.pathTestHint'
                : 'databaseMigration.connectionTestHint',
            )
          }}</span>
        </div>
        <ul v-if="ready" class="database-checks">
          <li v-for="check in state?.checks" :key="check">
            <span>✓</span>{{ t(`databaseMigration.checks.${check}`) }}
          </li>
        </ul>
        <p v-if="ready">
          {{
            t(
              state?.targetPermissions === 'catalog_confirmed'
                ? 'databaseMigration.catalogPermissions'
                : 'databaseMigration.pendingPermissions',
            )
          }}
        </p>
        <template v-if="ready"
          ><AppButton
            variant="primary"
            :disabled="!ready"
            :loading="busy"
            @click="action('start')"
            >{{ t('databaseMigration.start') }}</AppButton
          ></template
        >
      </form>
      <AppButton
        v-if="!recovery && state?.phase === 'quiescing'"
        @click="router.push('/database-recovery')"
        >{{ t('databaseMigration.recovery') }}</AppButton
      >
    </template>
  </section>
</template>
<style scoped>
.database-probe-feedback {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
  margin-top: 8px;
  font-size: 12px;
  line-height: 1.5;
}
.database-probe-hint {
  color: var(--color-dim);
}
.database-probe-success {
  color: var(--color-green);
}
.database-probe-error {
  color: var(--color-red);
}

.database-engines {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}
.database-engines :deep(button) {
  white-space: normal;
  text-align: left;
  justify-content: flex-start;
  min-width: 0;
}
.database-fields {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1fr);
  gap: 14px;
}
.database-advanced[open] summary {
  margin-bottom: 14px;
}
.database-metadata {
  display: grid;
  grid-template-columns: 1fr 2fr 2fr;
  gap: 12px;
  margin: 0;
  padding-top: 14px;
  border-top: 1px solid var(--color-border);
}
.database-metadata dt {
  font-size: 12px;
  color: var(--color-dim);
}
.database-metadata dd {
  margin: 4px 0 0;
  font-family: var(--font-mono);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.database-stages {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  padding: 0;
  list-style: none;
  gap: 8px;
}
.database-stages li {
  display: flex;
  align-items: center;
  gap: 7px;
  color: var(--color-dim);
  font-size: 12px;
}
.database-stages span {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border: 1px solid var(--color-border-strong);
  border-radius: 50%;
  flex-shrink: 0;
}
.database-stages .done {
  color: var(--color-green);
}
.database-stages .current {
  color: var(--color-primary);
}
.database-stages .current span {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}
.database-checks {
  list-style: none;
  padding: 0;
  margin: 0;
}
.database-checks li {
  display: flex;
  gap: 8px;
  align-items: center;
}
.database-checks span {
  color: var(--color-green);
}
@media (max-width: 640px) {
  .database-fields,
  .database-metadata {
    grid-template-columns: 1fr;
  }
  .database-engines,
  .database-stages {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

.database-page {
  display: grid;
  gap: var(--settings-page-gap);
  max-width: 880px;
}
.database-recovery {
  margin: clamp(16px, 5vw, 64px) auto;
  padding: 0 20px;
}
.database-page h2 {
  font-size: var(--settings-title-size);
  margin: 0 0 8px;
}
.database-page h3 {
  font-size: var(--settings-panel-title-size);
  margin: 0;
}
.database-page p {
  color: var(--color-dim);
  font-size: var(--settings-text-size);
  line-height: 1.65;
  margin: 0;
}
.database-panel {
  display: grid;
  gap: var(--settings-form-gap);
  padding: var(--settings-panel-padding);
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: 10px;
  min-width: 0;
}
.database-page input:not([type='checkbox']) {
  width: 100%;
  box-sizing: border-box;
  height: var(--settings-control-height);
  color: var(--color-text);
  background: var(--color-inset);
  border: 1px solid var(--color-border-strong);
  border-radius: 6px;
  padding: 0 10px;
}
.database-actions,
.database-summary {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.database-summary > span:first-child {
  color: var(--color-dim);
}
.database-check {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: var(--settings-text-size);
  line-height: 1.6;
}
.database-check input {
  margin-top: 5px;
  flex-shrink: 0;
}
.database-error {
  color: var(--color-danger) !important;
}
.database-table {
  max-height: 320px;
  overflow: auto;
  margin-top: 12px;
}
.database-table table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
.database-table th,
.database-table td {
  text-align: left;
  padding: 7px;
  border-bottom: 1px solid var(--color-border);
}
.database-table td:last-child,
.database-table th:last-child {
  text-align: right;
}
summary {
  cursor: pointer;
  font-size: 13px;
}
li {
  font-size: 13px;
  margin: 4px 0;
}
</style>
