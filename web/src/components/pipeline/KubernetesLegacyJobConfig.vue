<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import type { PipelineJob, PipelineStage } from "../../api/pipeline";
import {
  kubeAPI,
  kubeError,
  type KubeConnection,
  type KubeGrant,
} from "../../api/kubernetes";
import { kubernetesSources, isKubernetesStage } from "./kubernetesConfig";
import FormField from "../ui/FormField.vue";
import AppButton from "../ui/AppButton.vue";
import "../../styles/kubernetes.css";
const props = defineProps<{
  config: Record<string, string>;
  stages: PipelineStage[];
  stage: PipelineStage;
  job: PipelineJob;
  projectId?: string;
}>();
const emit = defineEmits<{ update: [Record<string, string>] }>(),
  { t } = useI18n();
const connections = ref<KubeConnection[]>([]),
  grants = ref<KubeGrant[]>([]),
  loading = ref(false),
  error = ref(""),
  grantId = ref("");
const connection = computed(() =>
    connections.value.find((c) => c.id === props.config.connectionId),
  ),
  eligible = computed(() =>
    grants.value.filter((g) => g.enabled && g.projectId === props.projectId),
  );
const selected = computed(() =>
    eligible.value.find((g) => g.id === grantId.value),
  ),
  sources = computed(() =>
    kubernetesSources(props.stages, props.stage, props.job),
  );
let controller = new AbortController(),
  generation = 0,
  alive = true;
function set(key: string, value: string) {
  emit("update", { ...props.config, [key]: value });
}
function chooseConnection(e: Event) {
  const key = (e.target as HTMLSelectElement).value,
    c = connections.value.find((c) => c.id === key);
  grantId.value = "";
  emit("update", {
    artifactSource: props.config.artifactSource ?? "",
    timeoutSeconds: props.config.timeoutSeconds || "300",
    connectionId: key,
    credentialId: c?.credentialId ?? "",
    context: c?.context ?? "",
    namespace: "",
    deployment: "",
    container: "",
    executor: "",
  });
}
function chooseGrant(e: Event) {
  grantId.value = (e.target as HTMLSelectElement).value;
  const g = selected.value;
  if (!g) return;
  emit("update", {
    ...props.config,
    namespace: g.namespace,
    deployment: g.deployment,
    container: "",
    executor: JSON.stringify(
      g.executor.kind === "local"
        ? { kind: "local" }
        : { kind: "ssh", serverId: g.executor.serverId },
    ),
  });
}
function match() {
  const c = props.config;
  grantId.value =
    eligible.value.find(
      (g) =>
        g.namespace === c.namespace &&
        g.deployment === c.deployment &&
        g.containers.includes(c.container) &&
        JSON.stringify(
          g.executor.kind === "local"
            ? { kind: "local" }
            : { kind: "ssh", serverId: g.executor.serverId },
        ) === c.executor,
    )?.id ?? "";
}
async function load() {
  controller.abort();
  controller = new AbortController();
  const signal = controller.signal,
    n = ++generation,
    key = props.config.connectionId;
  loading.value = true;
  error.value = "";
  grants.value = [];
  try {
    const list = await kubeAPI.connections(signal);
    if (!alive || signal.aborted || n !== generation) return;
    connections.value = list;
    if (key) {
      const data = await kubeAPI.grants(key, signal);
      if (!alive || signal.aborted || n !== generation) return;
      grants.value = data;
      match();
    }
  } catch (e) {
    if (alive && !signal.aborted && n === generation)
      error.value = kubeError(e);
  } finally {
    if (alive && n === generation) loading.value = false;
  }
}
watch(() => [props.config.connectionId, props.projectId], load);
onMounted(load);
onBeforeUnmount(() => {
  alive = false;
  controller.abort();
});
</script>
<template>
  <section class="kube-surface kube-form">
    <p v-if="!isKubernetesStage(stage)" class="kube-error" role="alert">
      {{ t("kubernetes.separateStage") }}
    </p>
    <p v-if="error" class="kube-error" role="alert">
      {{ t(error) }}
      <AppButton @click="load">{{ t("common.refresh") }}</AppButton>
    </p>
    <p v-if="loading" role="status">{{ t("kubernetes.loading") }}</p>
    <fieldset :disabled="loading" class="kube-form">
      <legend>{{ t("kubernetes.target") }}</legend>
      <FormField :label="t('kubernetes.connection')" field-id="kj-connection"
        ><select
          id="kj-connection"
          :value="config.connectionId || ''"
          @change="chooseConnection"
        >
          <option value="">{{ t("kubernetes.select") }}</option>
          <option
            v-for="c in connections"
            :key="c.id"
            :value="c.id"
            :disabled="!c.enabled"
          >
            {{ c.name }} · {{ c.context }}
          </option>
          <option
            v-if="config.connectionId && !connection"
            :value="config.connectionId"
            disabled
          >
            {{ t("kubernetes.unavailableChoice") }}
          </option>
        </select></FormField
      >
      <FormField :label="t('kubernetes.target')" field-id="kj-target"
        ><select id="kj-target" :value="grantId" @change="chooseGrant">
          <option value="">{{ t("kubernetes.select") }}</option>
          <option v-for="g in eligible" :key="g.id" :value="g.id">
            {{ g.environment }} · {{ g.namespace }} / {{ g.deployment }} ·
            {{ g.executor.kind === "local" ? t("kubernetes.local") : "SSH" }}
          </option>
        </select></FormField
      >
      <p v-if="!loading && connection && !eligible.length" class="kube-note">
        {{ t("kubernetes.noProjectGrant") }}
      </p>
      <p v-if="config.namespace && !grantId && !loading" class="kube-note">
        {{ t("kubernetes.staleTarget") }} · {{ config.namespace }}/{{
          config.deployment
        }}
      </p>
      <FormField :label="t('kubernetes.container')" field-id="kj-container"
        ><select
          id="kj-container"
          :value="config.container || ''"
          @change="set('container', ($event.target as HTMLSelectElement).value)"
        >
          <option value="">{{ t("kubernetes.select") }}</option>
          <option v-for="c in selected?.containers ?? []" :key="c">
            {{ c }}
          </option>
        </select></FormField
      >
      <p v-if="selected?.requireApproval" class="kube-muted">
        {{ t("kubernetes.approvalRequired") }}
      </p>
    </fieldset>
    <FormField
      :label="t('kubernetes.imageSource')"
      field-id="kj-source"
      :hint="t('kubernetes.imageSourceHint')"
      ><select
        id="kj-source"
        :value="config.artifactSource || ''"
        @change="
          set('artifactSource', ($event.target as HTMLSelectElement).value)
        "
      >
        <option value="">{{ t("kubernetes.select") }}</option>
        <option v-for="s in sources" :key="s.selector" :value="s.selector">
          {{ s.stage.name }} / {{ s.job.name }}
        </option>
        <option
          v-if="
            config.artifactSource &&
            !sources.some((s) => s.selector === config.artifactSource)
          "
          :value="config.artifactSource"
          disabled
        >
          {{ t("kubernetes.unavailableChoice") }}
        </option>
      </select></FormField
    >
    <p v-if="!sources.length" class="kube-note">
      {{ t("kubernetes.noPushSource") }}
    </p>
    <details class="kube-details">
      <summary>{{ t("kubernetes.more") }}</summary>
      <div class="kube-form">
        <FormField :label="t('kubernetes.timeout')" field-id="kj-timeout"
          ><input
            id="kj-timeout"
            type="number"
            min="30"
            max="1800"
            step="1"
            :value="config.timeoutSeconds || '300'"
            @change="
              set('timeoutSeconds', ($event.target as HTMLInputElement).value)
            "
        /></FormField>
        <dl class="kube-detail">
          <dt>{{ t("kubernetes.context") }}</dt>
          <dd>{{ config.context || "—" }}</dd>
          <dt>{{ t("kubernetes.credential") }}</dt>
          <dd>{{ config.credentialId || "—" }}</dd>
        </dl>
      </div>
    </details>
    <RouterLink to="/settings/kubernetes">{{
      t("kubernetes.manageConnections")
    }}</RouterLink>
  </section>
</template>
