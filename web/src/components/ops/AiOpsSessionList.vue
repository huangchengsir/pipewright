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
  <section class="ops-section">
    <h3 class="list-title">{{ t('opsChat.history') }}</h3>
    <div class="session-list">
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
    <form v-if="editing" class="ops-row" @submit.prevent="save">
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
    <div v-else class="ops-row">
      <button class="ops-btn" :disabled="disabled" @click="start">
        <Pencil />{{ t('opsChat.rename') }}
      </button>
      <button
        class="ops-btn ops-danger"
        :disabled="disabled"
        @click="emit('remove')"
      >
        <Trash />{{ t('opsChat.delete') }}
      </button>
    </div>
  </section>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.list-title {
  font-size: 13px;
  margin: 0 0 8px;
}
.session-list {
  max-height: 220px;
  overflow: auto;
  margin-bottom: 10px;
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
</style>
