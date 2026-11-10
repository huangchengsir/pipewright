<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { PipelineStage } from "../../api/pipeline";
import FormField from "../ui/FormField.vue";
import type { ReleaseSource } from "./kubernetesReleaseConfig";

const props = defineProps<{
  modelValue: ReleaseSource;
  stages: PipelineStage[];
  chart?: boolean;
  prefix: string;
}>();
const emit = defineEmits<{ "update:modelValue": [ReleaseSource] }>();
const { t } = useI18n();
const jobs = computed(
  () =>
    props.stages.find((s) => s.id === props.modelValue.artifact?.stageId)
      ?.jobs ?? [],
);
function kind(value: string) {
  emit(
    "update:modelValue",
    value === "inline"
      ? { kind: value, content: "" }
      : value === "git"
        ? { kind: value, path: "" }
        : {
            kind: value,
            artifact: { stageId: "", jobId: "", declarationIndex: 0 },
          },
  );
}
function field(key: "path" | "content", value: string) {
  emit("update:modelValue", { ...props.modelValue, [key]: value });
}
function artifact(key: string, value: string) {
  emit("update:modelValue", {
    ...props.modelValue,
    artifact: {
      stageId: "",
      jobId: "",
      declarationIndex: 0,
      ...props.modelValue.artifact,
      [key]: key === "declarationIndex" ? Number(value) : value,
    },
  });
}
const variableExample = "$" + "{NAME}",
  literalExample = "$" + "$" + "{NAME}";
</script>
<template>
  <div class="kube-form">
    <FormField
      :label="t('kubernetes.release.source')"
      :field-id="prefix + '-source'"
      ><select
        :id="prefix + '-source'"
        :value="modelValue.kind"
        @change="kind(($event.target as HTMLSelectElement).value)"
      >
        <option v-if="!chart" value="inline">
          {{ t("kubernetes.release.inline") }}
        </option>
        <option value="git">{{ t("kubernetes.release.git") }}</option>
        <option value="artifact">{{ t("kubernetes.release.artifact") }}</option>
      </select></FormField
    >
    <FormField
      v-if="modelValue.kind === 'inline'"
      :label="chart ? 'YAML' : t('kubernetes.release.yaml')"
      :field-id="prefix + '-yaml'"
      :hint="
        t('kubernetes.release.variableHint', {
          variable: variableExample,
          literal: literalExample,
        })
      "
    >
      <textarea
        :id="prefix + '-yaml'"
        class="kube-code"
        :value="modelValue.content"
        rows="12"
        spellcheck="false"
        @input="field('content', ($event.target as HTMLTextAreaElement).value)"
      />
    </FormField>
    <template v-if="modelValue.kind === 'artifact'"
      ><div class="kube-grid">
        <FormField
          :label="t('kubernetes.release.stage')"
          :field-id="prefix + '-stage'"
          ><select
            :id="prefix + '-stage'"
            :value="modelValue.artifact?.stageId"
            @change="
              artifact('stageId', ($event.target as HTMLSelectElement).value)
            "
          >
            <option value="">{{ t("kubernetes.select") }}</option>
            <option v-for="s in stages" :key="s.id" :value="s.id">
              {{ s.name }}
            </option>
          </select></FormField
        ><FormField
          :label="t('kubernetes.release.job')"
          :field-id="prefix + '-job'"
          ><select
            :id="prefix + '-job'"
            :value="modelValue.artifact?.jobId"
            @change="
              artifact('jobId', ($event.target as HTMLSelectElement).value)
            "
          >
            <option value="">{{ t("kubernetes.select") }}</option>
            <option v-for="j in jobs" :key="j.id" :value="j.id">
              {{ j.name }}
            </option>
          </select></FormField
        ><FormField
          :label="t('kubernetes.release.declaration')"
          :field-id="prefix + '-index'"
          ><input
            :id="prefix + '-index'"
            type="number"
            min="0"
            :value="modelValue.artifact?.declarationIndex ?? 0"
            @change="
              artifact(
                'declarationIndex',
                ($event.target as HTMLInputElement).value,
              )
            "
        /></FormField></div
    ></template>
    <FormField
      v-if="modelValue.kind !== 'inline'"
      :label="t('kubernetes.release.path')"
      :field-id="prefix + '-path'"
      ><input
        :id="prefix + '-path'"
        :value="modelValue.path"
        @input="field('path', ($event.target as HTMLInputElement).value)"
    /></FormField>
  </div>
</template>
