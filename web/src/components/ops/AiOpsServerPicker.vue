<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Server } from '../../api/servers'
const props = withDefaults(
  defineProps<{
    servers: Server[]
    selected: string[]
    disabled: boolean
    limit?: number
  }>(),
  { limit: 8 },
)
const emit = defineEmits<{ change: [ids: string[]] }>()
const { t } = useI18n()
const expanded = shallowRef(!props.disabled && !props.selected.length)
watch(
  () => [props.selected.length, props.disabled] as const,
  ([count, disabled]) => {
    if (!disabled && count === 0) expanded.value = true
  },
)
const missing = computed(() =>
  props.selected.filter((id) => !props.servers.some((s) => s.id === id)),
)
function toggle(id: string): void {
  if (props.disabled) return
  emit(
    'change',
    props.selected.includes(id)
      ? props.selected.filter((x) => x !== id)
      : [...props.selected, id].slice(0, props.limit),
  )
}
</script>
<template>
  <section class="ops-section server-navigation">
    <details
      class="server-details"
      :open="expanded"
      @toggle="expanded = ($event.target as HTMLDetailsElement).open"
    >
      <summary class="server-summary">
        <span>{{ t('opsChat.selectedLimit', { count: selected.length, limit }) }}</span>
        <span class="server-chevron" aria-hidden="true"></span>
      </summary>
      <div class="server-list" tabindex="0" :aria-label="t('opsChat.targets')">
        <label v-for="server in servers" :key="server.id" class="server-choice">
          <input
            type="checkbox"
            :checked="selected.includes(server.id)"
            :disabled="
              disabled ||
              (selected.length >= limit && !selected.includes(server.id))
            "
            @change="toggle(server.id)"
          />
          <span class="ops-grow"
            ><strong class="ops-wrap">{{ server.name }}</strong
            ><span class="ops-muted ops-wrap"
              >{{ server.host }}:{{ server.port }}</span
            ></span
          >
        </label>
        <label v-for="id in missing" :key="id" class="server-choice ops-error">
          <input
            type="checkbox"
            checked
            :disabled="disabled"
            @change="toggle(id)"
          />{{ t('opsChat.missingServer') }} · {{ id }}
        </label>
        <p v-if="!servers.length" class="ops-muted">
          {{ t('opsChat.noServers') }}
        </p>
      </div>
    </details>
    <p class="target-summary ops-muted ops-wrap" tabindex="0">
      {{
        selected.length
          ? selected
              .map(
                (id) =>
                  servers.find((s) => s.id === id)?.name ??
                  t('opsChat.missingServer'),
              )
              .join(' · ')
          : t('opsChat.noTargets')
      }}
    </p>
  </section>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.server-navigation {
  display: flex;
  flex-direction: column;
  flex: none;
  min-height: 0;
}
.server-details {
  flex: none;
}
.server-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: fit-content;
  padding: 5px 10px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  background: var(--color-card-2);
  cursor: pointer;
  min-height: 34px;
  overflow-wrap: anywhere;
}
.server-summary::-webkit-details-marker { display: none; }
.server-chevron {
  width: 7px;
  height: 7px;
  flex: none;
  border-inline-end: 1.5px solid;
  border-block-end: 1.5px solid;
  color: var(--color-dim);
  transform: translateY(-2px) rotate(45deg);
}
.server-details[open] .server-chevron {
  color: var(--color-primary);
  transform: translateY(2px) rotate(225deg);
}
@media (max-width: 600px), (pointer: coarse) {
  .server-summary { min-height: 44px; }
}
.server-list {
  /* Native details content does not reliably participate in flex shrinking. */
  height: var(--ops-list-max-height, 200px);
  overflow: auto;
  overscroll-behavior: contain;
}
.server-choice {
  display: flex;
  gap: 10px;
  align-items: center;
  min-height: 44px;
  padding: 6px 0;
}
.server-choice input {
  width: 18px;
  height: 18px;
  flex: none;
}
.server-choice strong,
.server-choice span span {
  display: block;
}
.target-summary {
  flex: none;
  max-height: 4.2em;
  line-height: 1.4;
  overflow: auto;
  overscroll-behavior: contain;
  margin: 4px 0 0;
}
.server-summary:focus-visible,
.server-list:focus-visible,
.target-summary:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}
@container ops-panel (max-height: 600px) {
  .server-navigation {
    padding-block: 6px;
  }
}
</style>
