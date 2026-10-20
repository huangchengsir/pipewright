<script setup lang="ts">
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { NSelect } from 'naive-ui'
import { Plus } from '@vicons/tabler'
import AppButton from '../ui/AppButton.vue'
import FormField from '../ui/FormField.vue'
import type { CreateMCPToken } from '../../api/mcp'

const props = defineProps<{ projects: { id: string; name: string }[]; busy: boolean; error: string }>()
const emit = defineEmits<{ create: [input: CreateMCPToken] }>()
const { t } = useI18n()
const form = reactive<CreateMCPToken>({ name: '', projectIds: [], canTrigger: false, expiresInDays: 7 })
const options = computed(() => props.projects.map(p => ({ label: p.name, value: p.id })))
const valid = computed(() => form.name.trim().length > 0 && form.name.trim().length <= 80 && form.projectIds.length > 0 && form.projectIds.length <= 32 && Number.isInteger(form.expiresInDays) && form.expiresInDays >= 1 && form.expiresInDays <= 30)
function submit() {
  if (props.busy || !valid.value) return
  emit('create', { ...form, name: form.name.trim(), projectIds: [...form.projectIds] })
}
</script>

<template>
  <form class="mcp-form" @submit.prevent="submit">
    <FormField :label="t('settingsMCP.name')" field-id="mcp-name" required>
      <input id="mcp-name" v-model="form.name" class="mcp-input" maxlength="80" autocomplete="off" :disabled="busy" />
    </FormField>
    <FormField :label="t('settingsMCP.projects')" field-id="mcp-projects" :hint="t('settingsMCP.projectLimit')" required>
      <NSelect v-model:value="form.projectIds" multiple filterable :options="options" :max-tag-count="3" :disabled="busy" :placeholder="t('settingsMCP.projects')" :input-props="{ id: 'mcp-projects', 'aria-label': t('settingsMCP.projects') }" />
    </FormField>
    <div class="mcp-form__lower">
      <FormField :label="t('settingsMCP.expiryDays')" field-id="mcp-expiry" required>
        <input id="mcp-expiry" v-model.number="form.expiresInDays" class="mcp-input" type="number" min="1" max="30" step="1" :disabled="busy" />
      </FormField>
      <label class="mcp-permission"><input v-model="form.canTrigger" type="checkbox" :disabled="busy" />{{ t('settingsMCP.canTrigger') }}</label>
      <AppButton type="submit" variant="primary" :loading="busy" :disabled="!valid"><Plus class="mcp-icon" aria-hidden="true" />{{ t('settingsMCP.create') }}</AppButton>
    </div>
    <p v-if="form.canTrigger" class="mcp-warning" role="note">{{ t('settingsMCP.triggerWarning') }}</p>
    <p v-if="error" class="mcp-error" role="alert">{{ error }}</p>
  </form>
</template>

<style scoped>
.mcp-form { display: grid; grid-template-columns: minmax(180px, 1fr) minmax(200px, 2fr); gap: 18px; }
.mcp-input { width: 100%; min-width: 0; height: 36px; padding: 7px 11px; border: 1px solid var(--color-border); border-radius: 6px; color: var(--color-text); background: var(--color-inset); font: inherit; }
.mcp-input:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.mcp-form__lower { grid-column: 1 / -1; display: flex; align-items: end; gap: 20px; flex-wrap: wrap; }
.mcp-form__lower > :first-child { width: 160px; }
.mcp-permission { display: flex; align-items: center; gap: 8px; min-height: 36px; font-size: var(--text-label); }
.mcp-permission input { accent-color: var(--color-primary); }
.mcp-form__lower > :last-child { margin-left: auto; }
.mcp-icon { width: 16px; height: 16px; }
.mcp-warning, .mcp-error { grid-column: 1 / -1; margin: 0; font-size: var(--text-label); overflow-wrap: anywhere; }
.mcp-warning { color: var(--color-dim); }
.mcp-error { color: var(--color-danger, #bd2538); }
@media (max-width: 640px) { .mcp-form { grid-template-columns: minmax(0, 1fr); } .mcp-form__lower { gap: 14px; } .mcp-form__lower > :last-child { margin-left: 0; } }
</style>
