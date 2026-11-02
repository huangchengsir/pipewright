<script setup lang="ts">
import { useI18n } from "vue-i18n";
import AppButton from "../ui/AppButton.vue";
defineProps<{
  state: "loading" | "ready" | "empty" | "error" | "stale";
  updatedAt: number;
  pending: boolean;
  label: string;
  forbidden?: boolean;
}>();
defineEmits<{ retry: [] }>();
const { t, locale } = useI18n();
function time(at: number): string {
  return new Date(at).toLocaleTimeString(locale.value, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}
</script>
<template>
  <div class="dash-data" :data-state="state" :aria-busy="pending">
    <div
      v-if="state === 'loading' || state === 'error' || state === 'stale'"
      class="data-notice"
      :role="state === 'error' ? 'alert' : 'status'"
    >
      <span
        ><strong>{{ label }}</strong> ·
        {{ t(forbidden ? "runExperience.forbidden" : `runExperience.${state}`)
        }}<small v-if="updatedAt">{{
          t("runExperience.updatedAt", { time: time(updatedAt) })
        }}</small></span
      >
      <AppButton
        v-if="state !== 'loading'"
        :loading="pending"
        @click="$emit('retry')"
        >{{ t("runs.retry") }}</AppButton
      >
    </div>
    <slot v-if="state !== 'loading' && state !== 'error'" />
    <p
      v-if="(state === 'ready' || state === 'empty') && updatedAt"
      class="data-updated"
    >
      {{ t("runExperience.updatedAt", { time: time(updatedAt) }) }}
    </p>
  </div>
</template>
<style scoped>
.dash-data {
  min-width: 0;
}
.data-notice {
  display: flex;
  gap: 12px;
  align-items: center;
  justify-content: space-between;
  padding: 12px 14px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-card);
  font-size: 0.78rem;
  color: var(--color-text-soft);
  margin-bottom: 8px;
}
.data-notice small {
  display: block;
  font-size: 0.72rem;
  margin-top: 4px;
  color: var(--color-faint);
}
.data-updated {
  font-size: 0.68rem;
  color: var(--color-faint);
  text-align: right;
  margin: 5px 4px 0;
}
[data-state="error"] .data-notice {
  border-left: 3px solid var(--color-red);
}
[data-state="stale"] .data-notice {
  border-left: 3px solid var(--color-amber);
}
</style>
