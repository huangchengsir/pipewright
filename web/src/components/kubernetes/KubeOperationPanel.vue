<script setup lang="ts">
import "../../styles/kubernetes.css";
import { onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AppButton from "../ui/AppButton.vue";
import { kubeAPI, kubeError, type KubeRelease } from "../../api/kubernetes";
import { usePermission } from "../../composables/usePermission";
import { useIdentityRequestScope } from "../../composables/useAuthorizedRequestScope";
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
const readPermission = usePermission(() => ({
  action: "run.read",
  resourceId: props.runId,
}));
const visible = ref(true);
const scope = useIdentityRequestScope(() => {
  visible.value = false;
  controller.abort();
  release.value = undefined;
  loadError.value = "";
  emit("blocked", true);
});
let controller = new AbortController(),
  generation = 0;
async function load() {
  if (props.operationId || !scope.active() || !readPermission.allowed.value)
    return;
  loadError.value = "";
  const n = ++generation,
    run = props.runId,
    node = props.nodeId;
  try {
    const rows = await scope.run(() =>
      kubeAPI.releases(run, controller.signal, scope.requestOptions),
    );
    if (
      !scope.active() ||
      !readPermission.allowed.value ||
      controller.signal.aborted ||
      n !== generation
    )
      return;
    const matches = rows.filter(
      (r) =>
        r.runId === run &&
        r.nodeKey === node &&
        (!props.releaseId || r.id === props.releaseId),
    );
    release.value = matches.length === 1 ? matches[0] : undefined;
    if (matches.length > 1) {
      loadError.value = "kubernetes.errors.conflict";
      emit("blocked", true);
    } else if (!matches.length && (props.releaseId || props.version === 2)) {
      loadError.value = "kubernetes.errors.missing";
      emit("blocked", true);
    }
  } catch (e) {
    if (
      scope.active() &&
      readPermission.allowed.value &&
      !controller.signal.aborted &&
      n === generation
    ) {
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
watch(readPermission.allowed, (allowed) => {
  controller.abort();
  controller = new AbortController();
  generation++;
  release.value = undefined;
  loadError.value = "";
  emit("blocked", true);
  if (allowed) void load();
});
const timer = setInterval(() => {
  if (!release.value) void load();
}, 4000);
onBeforeUnmount(() => {
  clearInterval(timer);
  controller.abort();
});
</script>
<template>
  <template v-if="visible && readPermission.allowed.value">
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
</template>
