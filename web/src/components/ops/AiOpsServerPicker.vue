<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Server } from '../../api/servers'
const props = defineProps<{
  servers: Server[]
  selected: string[]
  disabled: boolean
}>()
const emit = defineEmits<{ change: [ids: string[]] }>()
const { t } = useI18n()
const missing = computed(() =>
  props.selected.filter((id) => !props.servers.some((s) => s.id === id)),
)
function toggle(id: string): void {
  if (props.disabled) return
  emit(
    'change',
    props.selected.includes(id)
      ? props.selected.filter((x) => x !== id)
      : [...props.selected, id].slice(0, 8),
  )
}
</script>
<template>
  <section class="ops-section">
    <details>
      <summary>{{ t('opsChat.targets') }} · {{ selected.length }}/8</summary>
      <div class="server-list">
        <label v-for="server in servers" :key="server.id" class="server-choice">
          <input
            type="checkbox"
            :checked="selected.includes(server.id)"
            :disabled="
              disabled ||
              (selected.length >= 8 && !selected.includes(server.id))
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
    <p class="target-summary ops-muted ops-wrap">
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
summary {
  cursor: pointer;
  min-height: 28px;
}
.server-list {
  max-height: 200px;
  overflow: auto;
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
  margin: 4px 0 0;
}
</style>
