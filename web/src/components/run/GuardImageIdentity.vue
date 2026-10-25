<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { GuardImage } from "../../api/releaseGuard";

const props = defineProps<{ image: GuardImage; compact?: boolean }>();
const { t } = useI18n();
const local = computed(() => props.image.digestKind === "config_id");
const value = computed(() => {
  if (local.value) return props.image.digest || "";
  return props.image.repository && props.image.digest
    ? `${props.image.repository}@${props.image.digest}`
    : props.image.reference || "";
});
</script>

<template>
  <span
    class="guard-image-identity"
    :data-identity-kind="local ? 'local' : 'registry'"
  >
    <span v-if="local" class="guard-image-identity__kind">{{
      t("releaseGuard.localImageId")
    }}</span>
    <details v-if="value && local && compact">
      <summary>{{ t("releaseGuardPresentation.technicalDetails") }}</summary>
      <code>{{ value }}</code>
    </details>
    <code v-else-if="value">{{ value }}</code>
    <span v-else>{{
      local
        ? t("releaseGuard.localImagePending")
        : t("releaseGuard.notProvided")
    }}</span>
    <span v-if="local" class="guard-image-identity__hint">{{
      t("releaseGuard.localImageScope")
    }}</span>
  </span>
</template>

<style scoped>
.guard-image-identity {
  display: grid;
  gap: 5px;
  overflow-wrap: anywhere;
}
.guard-image-identity code {
  font-family: var(--font-mono);
  font-size: 0.75rem;
}
.guard-image-identity__kind {
  font-weight: 600;
}
.guard-image-identity__hint {
  color: var(--color-dim);
  font-size: 0.75rem;
  line-height: 1.5;
}
summary {
  cursor: pointer;
  min-height: 36px;
  display: flex;
  align-items: center;
  color: var(--color-primary);
  font-size: 0.8rem;
}
summary:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}
@media (max-width: 900px) {
  summary {
    min-height: 44px;
  }
}
</style>
