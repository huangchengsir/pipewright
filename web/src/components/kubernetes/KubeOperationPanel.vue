<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch, useId } from "vue";
import { useI18n } from "vue-i18n";
import { kubeAPI, kubeError, type KubeOperation } from "../../api/kubernetes";
import { useKubeMutation } from "../../composables/useKubeMutation";
import { useConfirm } from "../../composables/useConfirm";
import {
  exactKubeOperation,
  kubeBlocksRetry,
  kubePhaseEvidence,
  kubePhases,
  type KubePhase,
} from "./kubernetesEvidence";
import AppButton from "../ui/AppButton.vue";
import KubeReadBack from "./KubeReadBack.vue";
import KubePassword from "./KubePassword.vue";
import "../../styles/kubernetes.css";
const props = defineProps<{
    runId: string;
    nodeId: string;
    operationId?: string;
    phaseLogs?: string[];
  }>(),
  emit = defineEmits<{ blocked: [boolean] }>(),
  { t } = useI18n(),
  confirm = useConfirm(),
  gate = useKubeMutation();
const { busy, error, uncertain } = gate,
  row = ref<KubeOperation>(),
  loading = ref(false),
  loadError = ref(""),
  phase = ref<KubePhase>("preflight");
const readBack = ref(false);
watch(uncertain, () => {
  readBack.value = false;
});
const phaseId = useId();
const evidence = computed(() => kubePhaseEvidence(props.phaseLogs ?? [])),
  success = computed(
    () =>
      row.value &&
      ["ready", "unchanged"].includes(row.value.business) &&
      row.value.cleanup === "verified",
  );
const noticeKey = computed(() => {
  const operation = row.value;
  if (operation?.business === "outcome_unknown")
    return "kubernetes.unknownHint";
  if (operation?.cleanup !== "verified") return "kubernetes.cleanupHint";
  if (
    operation.business === "failed" &&
    ["patch_not_authorized", "permission_check_unverified"].includes(
      operation.reasonCode,
    )
  )
    return "kubernetes.phaseCode." + operation.reasonCode;
  return "kubernetes.historyHint";
});
let controller = new AbortController(),
  seq = 0,
  alive = true;
async function load() {
  controller.abort();
  controller = new AbortController();
  const signal = controller.signal,
    n = ++seq,
    run = props.runId,
    node = props.nodeId;
  row.value = undefined;
  emit("blocked", true);
  loading.value = true;
  loadError.value = "";
  try {
    const data = await kubeAPI.operations(run, signal);
    if (!alive || signal.aborted || n !== seq) return;
    row.value = exactKubeOperation(data, run, node, props.operationId);
    if (uncertain.value) readBack.value = true;
    // An authoritative empty result can represent a failure before operation creation.
    // Only permit opening the server-side retry plan; missing pinned or ambiguous evidence stays blocked.
    const beforeOperation =
      !props.operationId &&
      !data.some((op) => op.runId === run && op.nodeId === node);
    emit("blocked", !beforeOperation && kubeBlocksRetry(row.value));
  } catch (e) {
    if (alive && !signal.aborted && n === seq) loadError.value = kubeError(e);
  } finally {
    if (alive && n === seq) loading.value = false;
  }
}
async function action(kind: "reconcile" | "retire") {
  const target = row.value;
  if (
    !target ||
    (kind === "retire" && !target.canRetire) ||
    (kind === "reconcile" && !target.canReconcile)
  )
    return;
  if (
    kind === "retire" &&
    !(await confirm.open({
      title: t("kubernetes.retire"),
      body: t("kubernetes.retireBody"),
      confirmLabel: t("kubernetes.retire"),
      variant: "danger",
    }))
  )
    return;
  if (!alive || row.value?.id !== target.id) return;
  gate.execute(async (signal) => {
    const result = await kubeAPI.observe(target, kind, signal);
    if (
      signal.aborted ||
      props.runId !== target.runId ||
      props.nodeId !== target.nodeId
    )
      return;
    row.value = result;
    emit("blocked", kubeBlocksRetry(result));
  });
}
function keydown(e: KeyboardEvent, index: number) {
  let next = index;
  if (e.key === "ArrowDown") next = (index + 1) % 4;
  else if (e.key === "ArrowUp") next = (index + 3) % 4;
  else if (e.key === "Home") next = 0;
  else if (e.key === "End") next = 3;
  else return;
  e.preventDefault();
  phase.value = kubePhases[next];
  const tabs = (
    e.currentTarget as HTMLElement
  ).parentElement?.querySelectorAll<HTMLButtonElement>("button");
  tabs?.[next]?.focus();
}
watch(
  () => [props.runId, props.nodeId, props.operationId],
  () => {
    gate.reset();
    phase.value = "preflight";
    void load();
  },
  { immediate: true },
);
onBeforeUnmount(() => {
  alive = false;
  controller.abort();
});
</script>
<template>
  <section class="kube-surface kube-operation">
    <header class="kube-head">
      <strong>{{ t("kubernetes.receipt") }}</strong
      ><AppButton :disabled="busy" :loading="loading" @click="load">{{
        t("common.refresh")
      }}</AppButton>
    </header>
    <p v-if="loadError" role="alert" class="kube-error">{{ t(loadError) }}</p>
    <p v-if="error" role="alert" class="kube-error">{{ t(error) }}</p>
    <p v-if="!loading && !row && !loadError" class="kube-note">
      {{ t("kubernetes.noReceipt") }}
    </p>
    <KubeReadBack
      v-if="uncertain"
      :loaded="readBack"
      :loading="loading"
      :records="row ? [row] : []"
      @read="load"
      @done="
        gate.reset();
        readBack = false;
      "
    /><template v-if="row"
      ><div class="kube-result">
        <span class="kube-status" :class="{ 'kube-status--good': success }">{{
          t("kubernetes.business." + row.business)
        }}</span
        ><span class="kube-status">{{
          t("kubernetes.cleanup." + row.cleanup)
        }}</span
        ><span v-if="row.retired" class="kube-status">{{
          t("kubernetes.retired")
        }}</span>
      </div>
      <p>{{ row.namespace }}/{{ row.deployment }} · {{ row.container }}</p>
      <p v-if="row.receipt" class="kube-muted">
        {{ t("kubernetes.replicas") }} {{ row.receipt.availableReplicas }} /
        {{ row.receipt.desiredReplicas }}
      </p>
      <p v-if="!success" class="kube-note">
        {{ t(noticeKey) }}
      </p>
      <div class="kube-actions">
        <AppButton
          v-if="row.canReconcile"
          :disabled="uncertain"
          :loading="busy"
          @click="action('reconcile')"
          >{{ t("kubernetes.reconcile") }}</AppButton
        ><AppButton
          v-if="row.canRetire"
          variant="ghost"
          :disabled="busy || uncertain"
          @click="action('retire')"
          >{{ t("kubernetes.retire") }}</AppButton
        >
      </div>
      <details class="kube-details">
        <summary>{{ t("kubernetes.identity") }}</summary>
        <dl class="kube-detail">
          <dt>{{ t("kubernetes.imageSource") }}</dt>
          <dd>{{ row.image }}</dd>
          <dt>{{ t("kubernetes.operation") }}</dt>
          <dd>{{ row.id }}</dd>
          <dt>{{ t("kubernetes.sourceRun") }}</dt>
          <dd>{{ row.runId }}</dd>
          <dt>{{ t("kubernetes.intent") }}</dt>
          <dd>{{ row.intentHash }}</dd>
          <dt>UID</dt>
          <dd>{{ row.receipt?.uid ?? "—" }}</dd>
          <dt>{{ t("kubernetes.revision") }}</dt>
          <dd>{{ row.receipt?.revision ?? "—" }}</dd>
          <dt>{{ t("kubernetes.reason") }}</dt>
          <dd>{{ row.reasonCode || "—" }}</dd>
        </dl>
      </details>
    </template>
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
          role="tab"
          type="button"
          :aria-selected="phase === item"
          :tabindex="phase === item ? 0 : -1"
          :aria-controls="phaseId + 'output'"
          @click="phase = item"
          @keydown="keydown($event, index)"
        >
          {{ t("kubernetes.phase." + item)
          }}<small>{{
            t(
              evidence[item].length
                ? "kubernetes.recorded"
                : "kubernetes.notRecorded",
            )
          }}</small>
        </button>
      </div>
      <div
        :id="phaseId + 'output'"
        class="kube-phase-output"
        role="tabpanel"
        :aria-labelledby="phaseId + phase"
        tabindex="0"
      >
        <p v-if="!evidence[phase].length">
          {{ t("kubernetes.notRecordedHint") }}
        </p>
        <slot :phase="phase"
          ><p v-for="code in evidence[phase]" :key="code">
            {{ t("kubernetes.phaseCode." + code) }}
          </p></slot
        >
      </div>
    </div>
    <KubePassword :state="gate" />
  </section>
</template>
<style scoped>
.kube-operation {
  margin: 12px 0;
}
.kube-result {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.kube-phase-layout {
  display: grid;
  grid-template-columns: 120px minmax(0, 1fr);
  gap: 10px;
  margin-top: 14px;
}
.kube-phase-tabs {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.kube-phase-tabs button {
  border: 1px solid transparent;
  background: transparent;
  border-radius: var(--rounded);
  color: var(--color-dim);
  padding: 8px;
  text-align: left;
  font: 12px var(--font-sans);
  cursor: pointer;
}
.kube-phase-tabs small {
  display: block;
  font-size: 10px;
  opacity: 0.8;
  margin-top: 3px;
}
.kube-phase-tabs button[aria-selected="true"] {
  background: var(--color-primary-soft);
  border-color: var(--color-primary);
  color: var(--color-text);
}
.kube-phase-tabs button:focus-visible,
.kube-phase-output:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}
.kube-phase-output {
  padding: 0;
  border-radius: var(--rounded);
  font: 12px/1.8 var(--font-sans);
  min-width: 0;
  overflow-wrap: anywhere;
}
@media (max-width: 639px) {
  .kube-phase-layout {
    grid-template-columns: 80px minmax(0, 1fr);
  }
  .kube-phase-tabs button {
    min-height: 44px;
  }
}
</style>
