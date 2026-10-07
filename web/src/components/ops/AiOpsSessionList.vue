<script setup lang="ts">
import { shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Pencil, Trash, Check, X } from '@vicons/tabler'
import type { Session } from '../../api/opsChat'
const props = defineProps<{
  sessions: Session[]
  currentId: string
  disabled: boolean
}>()
const emit = defineEmits<{
  select: [id: string]
  rename: [title: string]
  remove: []
}>()
const { t } = useI18n()
const editing = shallowRef(false),
  title = shallowRef('')
let editingId = ''
watch(
  () => props.currentId,
  () => {
    editing.value = false
    title.value = ''
    editingId = ''
  },
)
function start(): void {
  editingId = props.currentId
  title.value =
    props.sessions.find((s) => s.id === props.currentId)?.title ?? ''
  editing.value = true
}
function save(): void {
  if (props.disabled || editingId !== props.currentId) return
  if (title.value.trim() && Array.from(title.value).length <= 80) {
    emit('rename', title.value.trim())
    editing.value = false
  }
}
</script>
<template>
  <section class="ops-section session-navigation">
    <div class="session-controls">
      <h3 v-if="!editing" class="list-title">{{ t('opsChat.history') }}</h3>
      <form v-if="editing" class="ops-row session-actions" @submit.prevent="save">
        <input
          v-model="title"
          class="ops-input ops-grow"
          maxlength="80"
          :aria-label="t('opsChat.title')"
        />
        <button
          class="ops-btn ops-icon"
          :title="t('opsChat.save')"
          :aria-label="t('opsChat.save')"
          :disabled="disabled"
        >
          <Check />
        </button>
        <button
          type="button"
          class="ops-btn ops-icon"
          :title="t('opsChat.cancel')"
          :aria-label="t('opsChat.cancel')"
          @click="editing = false"
        >
          <X />
        </button>
      </form>
      <div v-else class="ops-row session-actions">
        <button
          class="ops-btn ops-icon"
          :title="t('opsChat.rename')"
          :aria-label="t('opsChat.rename')"
          :disabled="disabled"
          @click="start"
        >
          <Pencil />
        </button>
        <button
          class="ops-btn ops-icon ops-danger"
          :title="t('opsChat.delete')"
          :aria-label="t('opsChat.delete')"
          :disabled="disabled"
          @click="emit('remove')"
        >
          <Trash />
        </button>
      </div>
    </div>
    <div class="session-list" tabindex="0" :aria-label="t('opsChat.history')">
      <button
        v-for="item in sessions"
        :key="item.id"
        class="session-row"
        :aria-current="item.id === currentId ? 'true' : undefined"
        :disabled="disabled"
        @click="emit('select', item.id)"
      >
        <strong class="ops-wrap">{{
          item.title || t('opsChat.newSession')
        }}</strong>
        <time class="ops-muted">{{
          new Date(item.updatedAt).toLocaleString()
        }}</time>
      </button>
    </div>
  </section>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.session-navigation {
  display: flex;
  flex-direction: column;
  flex: 0 1 auto;
  gap: 8px;
  min-height: 0;
}
.session-controls {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
  min-width: 0;
}
.session-actions {
  flex-wrap: nowrap;
}
form.session-actions {
  flex: 1;
}
.list-title {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
  font-size: 13px;
  margin: 0;
}
.session-list {
  flex: 0 1 auto;
  min-height: 0;
  max-height: var(--ops-list-max-height, 220px);
  overflow: auto;
  overscroll-behavior: contain;
}
.session-list:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}
.session-row {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 4px;
  text-align: left;
  padding: 10px;
  background: transparent;
  color: var(--color-text);
  border: 0;
  border-bottom: 1px solid var(--color-border);
  cursor: pointer;
}
.session-row[aria-current='true'] {
  background: var(--color-primary-soft);
  border-left: 3px solid var(--color-primary);
}
@container ops-panel (max-height: 600px) {
  .session-navigation {
    padding-block: 6px;
    gap: 4px;
  }
}
</style>
