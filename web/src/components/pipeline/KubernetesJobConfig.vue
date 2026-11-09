<script setup lang="ts">
import { useI18n } from "vue-i18n";
import type { PipelineJob, PipelineStage } from "../../api/pipeline";
import AppButton from "../ui/AppButton.vue";
import Legacy from "./KubernetesLegacyJobConfig.vue";
import Release from "./KubernetesReleaseJobConfig.vue";
import { newReleaseConfig } from "./kubernetesReleaseConfig";
const props = defineProps<{
  config: Record<string, string>;
  stages: PipelineStage[];
  stage: PipelineStage;
  job: PipelineJob;
  projectId?: string;
}>();
const emit = defineEmits<{ update: [Record<string, string>] }>();
const { t } = useI18n();
</script>
<template>
  <Release
    v-if="String(config.version) === '2'"
    v-bind="props"
    @update="emit('update', $event)"
  /><template v-else
    ><Legacy v-bind="props" @update="emit('update', $event)" />
    <details class="kube-surface">
      <summary>{{ t("kubernetes.release.legacy") }}</summary>
      <p>{{ t("kubernetes.release.upgradeHint") }}</p>
      <AppButton @click="emit('update', newReleaseConfig())">{{
        t("kubernetes.release.useSuite")
      }}</AppButton>
    </details></template
  >
</template>
