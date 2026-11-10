<script setup lang="ts">
import "../../styles/kubernetes.css";
import { onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AppButton from "../ui/AppButton.vue";
import { kubeAPI, kubeError, type KubeRelease } from "../../api/kubernetes";
import Legacy from "./KubeLegacyOperationPanel.vue";
import Release from "./KubeReleasePanel.vue";
const props = defineProps<{
  runId: string;
  nodeId: string;
  operationId?: string;
  releaseId?: string;
  version?: number;
  phaseLogs?: string[];
}>();
const emit = defineEmits<{ blocked: [boolean]; accepted: [string] }>();
const { t } = useI18n();
const release = ref<KubeRelease>(),
  loadError = ref("");
let controller = new AbortController(),
  generation = 0;
async function load() {
  if (props.operationId) return;
  loadError.value = "";
  const n = ++generation,
    run = props.runId,
    node = props.nodeId;
  try {
    const rows = await kubeAPI.releases(run, controller.signal);
    if (controller.signal.aborted || n !== generation) return;
    const matches = rows.filter(
      (r) =>
        r.runId === run &&
        r.nodeKey === node &&
        (!props.releaseId || r.id === props.releaseId),
    );
    release.value = matches.length === 1 ? matches[0] : undefined;
    if (matches.length > 1) { loadError.value='kubernetes.errors.conflict'; emit('blocked',true) }
    else if (!matches.length && (props.releaseId || props.version === 2)) { loadError.value='kubernetes.errors.missing'; emit('blocked',true) }
  } catch (e) {
    if (!controller.signal.aborted && n === generation) {
      loadError.value = kubeError(e);
      emit("blocked", true);
    }
  }
}
watch(
  () => [props.runId, props.nodeId, props.operationId, props.releaseId],
  () => {
    controller.abort();
    controller = new AbortController();
    release.value = undefined;
    void load();
  },
  { immediate: true },
);
const timer = setInterval(() => {
  if (!release.value) void load();
}, 4000);
onBeforeUnmount(() => {
  clearInterval(timer);
  controller.abort();
});
</script>
<template>
  <Release
    v-if="release"
    :release="release"
    @blocked="emit('blocked', $event)"
    @accepted="emit('accepted', $event)"
    ><template v-if="$slots.default" #default="slot"
      ><slot v-bind="slot" /></template
  ></Release>
  <p v-else-if="loadError" class="kube-error" role="alert">
    {{ t(loadError)
    }}<AppButton @click="load">{{ t("common.refresh") }}</AppButton>
  </p>
  <Legacy
    v-else-if="!releaseId && version !== 2"
    v-bind="props"
    @blocked="emit('blocked', $event)"
    ><template v-if="$slots.default" #default="slot"
      ><slot v-bind="slot" /></template
  ></Legacy>
  <p v-else role="status">…</p>
</template>
