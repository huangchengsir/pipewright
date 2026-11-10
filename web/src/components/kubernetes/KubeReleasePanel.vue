<script setup lang="ts">
import "../../styles/kubernetes.css";
import { computed, onBeforeUnmount, ref, useId, watch } from "vue";
import { useI18n } from "vue-i18n";
import { RouterLink } from "vue-router";
import { kubeAPI, kubeError, kubeUnknownWrite, type KubeRelease } from "../../api/kubernetes";
import { useKubeMutation } from "../../composables/useKubeMutation";
import { useConfirm } from "../../composables/useConfirm";
import AppButton from "../ui/AppButton.vue";
import KubePassword from "./KubePassword.vue";
import KubeReadBack from "./KubeReadBack.vue";
import { kubePhases, type KubePhase } from "./kubernetesEvidence";
const props = defineProps<{ release: KubeRelease }>(),
  emit = defineEmits<{ blocked: [boolean]; accepted: [string] }>();
const { t, te } = useI18n(),
  gate = useKubeMutation(),
  confirm = useConfirm();
const { busy, error, uncertain } = gate;
const row = ref(props.release),
  loading = ref(false),
  loadError = ref(""),
  readBack = ref(false),
  phase = ref<KubePhase>("preflight"),
  pendingRollback = ref(""),
  rollbackRun = ref("");
const phaseId = useId();
let controller = new AbortController(),
  generation = 0;
const pendingKey = () => "pipewright:kubernetes-rollback:" + row.value.id;
function remember(id: string) {
  pendingRollback.value = id;
  try {
    sessionStorage.setItem(pendingKey(), id);
  } catch {
    /* The server still retains the durable request. */
  }
}
function forget() {
  pendingRollback.value = "";
  try {
    sessionStorage.removeItem(pendingKey());
  } catch {
    /* Storage can be disabled. */
  }
}
const canRollback = computed(
    () =>
      row.value.ownerFenced &&
      row.value.cleanup === "verified" &&
      ["paused", "ready", "failed", "partial_failed"].includes(
        row.value.state,
      ) &&
      !row.value.preview.rollbackOf,
  ),
  canReconcile = computed(
    () =>
      row.value.ownerFenced &&
      row.value.cleanup === "verified" &&
      !["paused", "resume_requested", "rollback_in_progress"].includes(
        row.value.state,
      ),
  );
function block() {
  emit(
    "blocked",
    !row.value.ownerFenced ||
      row.value.cleanup !== "verified" ||
      [
        "outcome_unknown",
        "running",
        "paused",
        "resume_requested",
        "rollback_in_progress",
      ].includes(row.value.state),
  );
}
async function findRollback() {
  if (!pendingRollback.value) return;
  try {
    const found = await kubeAPI.rollbackRequest(
      row.value,
      pendingRollback.value,
      controller.signal,
    );
    if (controller.signal.aborted) return;
    rollbackRun.value = found.runId;
    forget();
    gate.reset();
  } catch (e) {
    if (!controller.signal.aborted) loadError.value = kubeError(e);
  }
}
async function load() {
  if (loading.value) return;
  const n = ++generation,
    target = row.value;
  loading.value = true;
  loadError.value = "";
  try {
    const fresh = await kubeAPI.release(target, controller.signal);
    if (
      controller.signal.aborted ||
      n !== generation ||
      props.release.id !== target.id
    )
      return;
    row.value = fresh;
    readBack.value = uncertain.value;
    block();
  } catch (e) {
    if (!controller.signal.aborted && n === generation)
      loadError.value = kubeError(e);
  } finally {
    if (n === generation) loading.value = false;
  }
}
async function action(kind: "continue" | "reconcile" | "cleanup" | "rollback") {
  const target = row.value;
  if (
    kind === "rollback" &&
    !(await confirm.open({
      title: t("kubernetes.release.rollback"),
      body: t("kubernetes.release.rollbackHint"),
      confirmLabel: t("kubernetes.release.rollback"),
      variant: "danger",
    }))
  )
    return;
  if (target.id !== row.value.id) return;
  const requestId = kind === "rollback" ? crypto.randomUUID() : "";
  gate.execute(async (signal) => {
    if (kind === "rollback") {
      remember(requestId);
      try {
        const next = await kubeAPI.rollbackRelease(target, requestId, signal);
        if (signal.aborted || row.value.id !== target.id) return;
        forget();
        rollbackRun.value = next.runId;
        emit("accepted", next.runId);
      } catch (error) {
        // A completed rejection proves no rollback was accepted. Lost replies,
        // request timeouts and navigation keep the original durable request ID.
        if (!signal.aborted && row.value.id === target.id && !kubeUnknownWrite(error)) forget();
        throw error;
      }
    } else {
      await kubeAPI.releaseAction(target, kind, signal);
      if (!signal.aborted && row.value.id === target.id) await load();
    }
  });
}
watch(
  () => props.release.id,
  () => {
    generation++;
    controller.abort();
    controller = new AbortController();
    gate.reset();
    row.value = props.release;
    loading.value = false;
    rollbackRun.value = "";
    pendingRollback.value = "";
    try {
      pendingRollback.value = sessionStorage.getItem(pendingKey()) ?? "";
    } catch {
      /* Optional storage. */
    }
    block();
    void load();
  },
  { immediate: true },
);
watch(
  () => props.release,
  (release) => {
    if (release.id === row.value.id && release.revision > row.value.revision) {
      row.value = release;
      block();
    }
  },
);
const timer = setInterval(() => {
  if (!busy.value) void load();
}, 4000);
onBeforeUnmount(() => {
  clearInterval(timer);
  controller.abort();
});
function keydown(e: KeyboardEvent, index: number) {
  let next = index;
  if (e.key === "ArrowDown") next = (index + 1) % 4;
  else if (e.key === "ArrowUp") next = (index + 3) % 4;
  else if (e.key === "Home") next = 0;
  else if (e.key === "End") next = 3;
  else return;
  e.preventDefault();
  phase.value = kubePhases[next];
  const tabs = (e.currentTarget as HTMLElement).parentElement?.querySelectorAll<HTMLButtonElement>("button");
  tabs?.[next]?.focus();
}
function state(value: string) {
  const key = "kubernetes.release.states." + value;
  return te(key) ? t(key) : t("kubernetes.release.checkFailed");
}
</script>
<template>
  <section class="kube-surface kube-release-result">
    <header class="kube-head">
      <strong
        >{{ t("kubernetes.receipt") }} ·
        {{
          t(
            "kubernetes.release.modes." +
              (row.preview.mode === "coredns"
                ? "manifest"
                : row.preview.mode === "rollback"
                  ? "image"
                  : row.preview.mode),
          )
        }}</strong
      ><AppButton :loading="loading" :disabled="busy" @click="load">{{
        t("common.refresh")
      }}</AppButton>
    </header>
    <p v-if="loadError" class="kube-error" role="alert">{{ t(loadError) }}</p>
    <p v-if="error" class="kube-error" role="alert">{{ t(error) }}</p>
    <div class="kube-actions">
      <strong>{{ state(row.state) }}</strong
      ><span>{{ t("kubernetes.cleanup." + row.cleanup) }}</span
      ><span v-if="row.preview.batchReplicas?.length"
        >{{ t("kubernetes.release.batch") }}
        {{ Math.min(row.cursor + 1, row.preview.batchReplicas.length) }} /
        {{ row.preview.batchReplicas.length }}</span
      >
    </div>
    <p v-if="row.state === 'outcome_unknown'" class="kube-note">
      {{ t("kubernetes.unknownHint") }}
    </p>
    <p v-if="row.cleanup !== 'verified'" class="kube-note">
      {{ t("kubernetes.cleanupHint") }}
    </p>
    <p v-if="row.state === 'paused'" class="kube-note">
      {{ t("kubernetes.release.pauseHint") }}
    </p>
    <div class="kube-actions">
      <AppButton
        v-if="row.state === 'paused'"
        :disabled="busy || uncertain || !!pendingRollback"
        @click="action('continue')"
        >{{ t("kubernetes.release.continue") }}</AppButton
      ><AppButton
        v-if="canRollback"
        :disabled="busy || uncertain || !!pendingRollback"
        @click="action('rollback')"
        >{{ t("kubernetes.release.rollback") }}</AppButton
      ><AppButton
        v-if="canReconcile"
        :disabled="busy || uncertain || !!pendingRollback"
        @click="action('reconcile')"
        >{{ t("kubernetes.reconcile") }}</AppButton
      ><AppButton
        v-if="row.ownerFenced && row.cleanup !== 'verified'"
        :disabled="busy || uncertain || !!pendingRollback"
        @click="action('cleanup')"
        >{{ t("kubernetes.release.cleanupTool") }}</AppButton
      >
    </div>
    <aside v-if="pendingRollback" class="kube-card">
      <p>{{ t("kubernetes.release.rollbackUnknown") }}</p>
      <AppButton :disabled="busy" @click="findRollback">{{
        t("kubernetes.release.findRollback")
      }}</AppButton
      ><code>{{ pendingRollback }}</code>
    </aside>
    <RouterLink
      v-if="rollbackRun"
      :to="{ name: 'run-detail', params: { id: rollbackRun } }"
      >{{ t("kubernetes.release.openRollback") }}</RouterLink
    >
    <details
      :open="
        ['failed', 'partial_failed', 'outcome_unknown'].includes(row.state)
      "
    >
      <summary>
        {{ t("kubernetes.release.resources") }} · {{ row.results.length }}
      </summary>
      <ul class="kube-resource-results">
        <li
          v-for="result in row.results"
          :key="
            [
              result.resource.group,
              result.resource.resource,
              result.resource.namespace,
              result.resource.name,
            ].join('/')
          "
        >
          <div>
            <strong
              >{{ result.resource.kind }} ·
              {{
                result.resource.namespace
                  ? result.resource.namespace + "/"
                  : ""
              }}{{ result.resource.name }}</strong
            ><span
              >{{ state(result.health.state)
              }}<template v-if="result.deleted">
                · {{ t("kubernetes.release.deleted") }}</template
              ></span
            >
          </div>
          <small v-if="result.observedAt"
            >{{ t("kubernetes.release.observed") }}
            {{ new Date(result.observedAt).toLocaleString() }}</small
          >
          <p class="kube-muted">
            {{
              result.health.state === "declared"
                ? t("kubernetes.release.declaredHint")
                : result.health.reason
            }}
          </p>
          <ul v-if="result.health.containers?.length">
            <li
              v-for="container in result.health.containers"
              :key="container.pod + '/' + container.name"
            >
              {{ container.pod }} / {{ container.name }} ·
              {{
                container.ready
                  ? t("kubernetes.release.states.ready")
                  : t("kubernetes.release.states.pending")
              }}<span v-if="container.restarts">
                · {{ t("kubernetes.release.restarts") }}
                {{ container.restarts }}</span
              >
            </li>
          </ul>
        </li>
      </ul>
    </details>
    <details>
      <summary>{{ t("kubernetes.identity") }}</summary>
      <dl class="kube-detail">
        <dt>{{ t("kubernetes.operation") }}</dt>
        <dd>{{ row.id }}</dd>
        <dt>{{ t("kubernetes.intent") }}</dt>
        <dd>{{ row.preview.planId }}</dd>
        <dt>{{ t("kubernetes.revision") }}</dt>
        <dd>{{ row.revision }}</dd>
        <dt v-if="row.preview.helmRelease">
          {{ t("kubernetes.release.helmRelease") }}
        </dt>
        <dd v-if="row.preview.helmRelease">
          {{ row.preview.helmRelease }} · {{ row.preview.helmRevision }}
        </dd>
        <dt v-if="row.preview.rollbackOf">
          {{ t("kubernetes.release.rollbackOf") }}
        </dt>
        <dd v-if="row.preview.rollbackOf">{{ row.preview.rollbackOf }}</dd>
      </dl>
      <ul v-if="row.preview.changes?.length">
        <li
          v-for="change in row.preview.changes"
          :key="
            change.resource.kind +
            change.resource.namespace +
            change.resource.name
          "
        >
          {{ change.resource.kind }} / {{ change.resource.name }} ·
          {{ t("kubernetes.release.actions." + change.action) }}
          <p v-for="image in change.images ?? []" :key="image.container">
            <code>{{ image.container }} · {{ image.image }}</code>
          </p>
        </li>
      </ul>
      <ul v-if="row.preview.deletes?.length">
        <li v-for="resource in row.preview.deletes" :key="resource.name">
          {{ t("kubernetes.delete") }} · {{ resource.kind }}/{{
            resource.namespace
          }}/{{ resource.name }}
        </li>
      </ul>
      <p
        v-for="retained in row.preview.retained ?? []"
        :key="retained.resource.name"
      >
        {{ t("kubernetes.release.retained") }} · {{ retained.resource.kind }}/{{
          retained.resource.name
        }}
        · {{ retained.reason }}
      </p>
    </details>
    <KubeReadBack
      v-if="uncertain && !pendingRollback"
      :loaded="readBack"
      :loading="loading"
      :records="[row]"
      @read="load"
      @done="
        gate.reset();
        readBack = false;
      "
    />
    <div v-if="$slots.default" class="kube-phase-layout">
      <div
        role="tablist"
        aria-orientation="vertical"
        :aria-label="t('kubernetes.phases')"
        class="kube-phase-tabs"
      >
        <button
          v-for="(item, index) in kubePhases"
          :id="phaseId + item"
          :key="item"
          type="button"
          role="tab"
          :aria-selected="phase === item"
          :tabindex="phase === item ? 0 : -1"
          :aria-controls="phaseId + 'output'"
          @click="phase = item"
          @keydown="keydown($event, index)"
        >
          {{ t(item === "update" ? "kubernetes.release.applyResources" : "kubernetes.phase." + item) }}
        </button>
      </div>
      <div
        :id="phaseId + 'output'"
        role="tabpanel"
        :aria-labelledby="phaseId + phase"
        tabindex="0"
        class="kube-phase-output"
      >
        <slot :phase="phase" />
      </div>
    </div>
    <KubePassword :state="gate" />
  </section>
</template>
