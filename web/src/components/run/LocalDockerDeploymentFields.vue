<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import {
  LOCAL_DOCKER_FIELDS,
  getJobTypeSpec,
} from "../pipeline/jobConfigSchema";
import AppSelect from "../ui/AppSelect.vue";

const props = defineProps<{
  modelValue: Record<string, string>;
  artifactType: string;
  strategy: string;
}>();
const emit = defineEmits<{
  "update:modelValue": [value: Record<string, string>];
}>();
const { t } = useI18n();
const fields = computed(() => {
  const config = { ...props.modelValue, artifactType: props.artifactType };
  return [
    ...LOCAL_DOCKER_FIELDS,
    ...(getJobTypeSpec("deploy_ssh")?.fields.filter((field) =>
      ["containerName", "ports"].includes(field.key),
    ) ?? []),
  ].filter((field) => !field.when || field.when(config));
});
function update(key: string, value: string): void {
  emit("update:modelValue", { ...props.modelValue, [key]: value });
}
</script>

<template>
  <fieldset class="local-docker-fields">
    <legend>{{ t("pipelineJob.localDockerLabel") }}</legend>
    <label
      v-for="field in fields"
      :key="field.key"
      :data-config-key="field.key"
    >
      <span>{{ field.label }}</span>
      <AppSelect
        v-if="field.kind === 'select'"
        :model-value="modelValue[field.key] ?? ''"
        :options="field.options ?? []"
        :aria-label="field.label"
        @update:model-value="update(field.key, $event)"
      />
      <textarea
        v-else-if="field.kind === 'textarea'"
        :value="modelValue[field.key] ?? ''"
        :placeholder="field.placeholder"
        :aria-label="field.label"
        rows="3"
        @input="update(field.key, ($event.target as HTMLTextAreaElement).value)"
      />
      <input
        v-else
        :value="modelValue[field.key] ?? ''"
        :placeholder="field.placeholder"
        :aria-label="field.label"
        type="text"
        @input="update(field.key, ($event.target as HTMLInputElement).value)"
      />
      <small v-if="field.hint">{{ field.hint }}</small>
    </label>
    <p
      v-if="modelValue.localDocker === 'true' && strategy === 'blue_green'"
      role="status"
    >
      {{ t("pipelineJob.localDockerStrategyConflict") }}
    </p>
    <p
      v-if="
        modelValue.localDocker === 'true' && !modelValue.containerName?.trim()
      "
      role="status"
    >
      {{ t("pipelineJob.localDockerContainerRequired") }}
    </p>
  </fieldset>
</template>

<style scoped>
.local-docker-fields {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  margin: 0;
  padding: 14px;
  display: grid;
  gap: 12px;
  min-width: 0;
}
legend {
  font-weight: 600;
  font-size: 0.85rem;
}
label {
  display: grid;
  gap: 6px;
  font-size: 0.8rem;
}
small {
  color: var(--color-dim);
  line-height: 1.5;
}
input,
textarea {
  box-sizing: border-box;
  width: 100%;
  min-height: 40px;
  padding: 8px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  background: var(--color-card-2);
  color: var(--color-text);
  font-family: var(--font-mono);
  font-size: 0.85rem;
}
textarea {
  resize: vertical;
}
p {
  margin: 0;
  color: var(--color-amber);
  font-size: 0.8rem;
  line-height: 1.5;
}
</style>
