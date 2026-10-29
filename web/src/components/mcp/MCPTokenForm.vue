<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NSelect } from 'naive-ui'
import { Plus } from '@vicons/tabler'
import AppButton from '../ui/AppButton.vue'
import FormField from '../ui/FormField.vue'
import { MCP_CONFIG_SECTIONS, MCP_CONFIG_OPERATIONS, type CreateMCPToken, type MCPConfigGrant, type MCPConfigSection, type MCPConfigOperation } from '../../api/mcp'

const props = defineProps<{ projects: { id: string; name: string }[]; busy: boolean; error: string }>()
const emit = defineEmits<{ create: [input: CreateMCPToken] }>()
const { t } = useI18n()
const form = reactive<CreateMCPToken>({ name: '', projectIds: [], canTrigger: false, expiresInDays: 7 })
const grants = reactive<Record<string, MCPConfigGrant>>({})
const options = computed(() => props.projects.map(p => ({ label: p.name, value: p.id })))
const selectedProjects = computed(() => props.projects.filter(p => form.projectIds.includes(p.id)))
const sectionOptions = computed(() => MCP_CONFIG_SECTIONS.map(value => ({ value, label: t(`mcpConfig.sections.${value}`) })))
const operationSection: Record<MCPConfigOperation, MCPConfigSection> = {
  'project.set_name': 'project', 'project.set_default_branch': 'project',
  'stage.rename': 'pipeline', 'job.rename': 'pipeline', 'step.set_limits': 'step_limits', 'build.set': 'build',
}
function operationOptions(id: string) {
  return MCP_CONFIG_OPERATIONS.filter(op => grants[id]?.sections.includes(operationSection[op])).map(value => ({ value, label: t(`mcpConfig.operations.${value.replaceAll('.', '_')}`) }))
}
const activeGrants = computed(() => form.projectIds.map(id => grants[id]).filter((grant): grant is MCPConfigGrant => !!grant?.read))
const valid = computed(() => form.name.trim().length > 0 && form.name.trim().length <= 80 && form.projectIds.length > 0 && form.projectIds.length <= 32 && Number.isInteger(form.expiresInDays) && form.expiresInDays >= 1 && form.expiresInDays <= 30 && activeGrants.value.every(g => g.sections.length > 0 && (!g.propose || g.operations.length > 0)))
watch(() => [...form.projectIds], ids => {
  for (const id of Object.keys(grants)) if (!ids.includes(id)) delete grants[id]
  for (const id of ids) if (!grants[id]) grants[id] = { projectId: id, read: false, propose: false, apply: false, sections: [], operations: [] }
}, { flush: 'sync' })
function setPermission(id: string, permission: 'read' | 'propose' | 'apply', event: Event) {
  const grant = grants[id]
  if (!grant || props.busy) return
  const checked = (event.target as HTMLInputElement).checked
  if (permission === 'read') {
    grant.read = checked
    grant.sections = checked ? [...MCP_CONFIG_SECTIONS] : []
    if (!checked) { grant.propose = false; grant.apply = false; grant.operations = [] }
  } else if (permission === 'propose' && grant.read) {
    grant.propose = checked
    grant.operations = checked ? operationOptions(id).map(op => op.value) : []
    if (!checked) grant.apply = false
  } else if (permission === 'apply' && grant.propose) grant.apply = checked
}
function setSections(id: string, values: MCPConfigSection[]) {
  if (props.busy) return
  const grant = grants[id]
  grant.sections = values
  grant.operations = grant.operations.filter(op => values.includes(operationSection[op]))
}
function submit() {
  if (props.busy || !valid.value) return
  emit('create', { ...form, name: form.name.trim(), projectIds: [...form.projectIds], ...(activeGrants.value.length ? { configGrants: activeGrants.value.map(g => ({ ...g, sections: [...g.sections], operations: [...g.operations] })) } : {}) })
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
    <fieldset v-if="selectedProjects.length" class="mcp-config-grants" :disabled="busy" aria-describedby="mcp-config-grants-hint">
      <legend>{{ t('mcpConfig.permissionsTitle') }}</legend>
      <p id="mcp-config-grants-hint" class="mcp-grants-hint">{{ t('mcpConfig.permissionsHint') }}</p>
      <div v-for="project in selectedProjects" :key="project.id" class="mcp-project-grant" :data-config-project="project.id">
        <strong>{{ project.name }}</strong>
        <div class="mcp-grant-toggles">
          <label><input type="checkbox" :checked="grants[project.id]?.read" :disabled="busy" data-permission="read" @change="setPermission(project.id, 'read', $event)" />{{ t('mcpConfig.read') }}</label>
          <label><input type="checkbox" :checked="grants[project.id]?.propose" :disabled="busy || !grants[project.id]?.read" data-permission="propose" @change="setPermission(project.id, 'propose', $event)" />{{ t('mcpConfig.propose') }}</label>
          <label><input type="checkbox" :checked="grants[project.id]?.apply" :disabled="busy || !grants[project.id]?.propose" data-permission="apply" @change="setPermission(project.id, 'apply', $event)" />{{ t('mcpConfig.apply') }}</label>
        </div>
        <div v-if="grants[project.id]?.read" class="mcp-grant-ranges">
          <FormField :label="t('mcpConfig.allowedSections')" :field-id="`mcp-sections-${project.id}`" required>
            <NSelect :value="grants[project.id].sections" multiple :options="sectionOptions" :disabled="busy" :input-props="{ id: `mcp-sections-${project.id}`, 'aria-label': t('mcpConfig.allowedSections') }" @update:value="setSections(project.id, $event)" />
          </FormField>
          <FormField v-if="grants[project.id].propose" :label="t('mcpConfig.allowedOperations')" :field-id="`mcp-operations-${project.id}`" required>
            <NSelect v-model:value="grants[project.id].operations" multiple :options="operationOptions(project.id)" :disabled="busy" :input-props="{ id: `mcp-operations-${project.id}`, 'aria-label': t('mcpConfig.allowedOperations') }" />
          </FormField>
        </div>
      </div>
      <p v-if="activeGrants.some(g => g.apply)" class="mcp-grants-hint" role="note">{{ t('mcpConfig.applyPermissionHint') }}</p>
    </fieldset>
    <p v-if="error" class="mcp-error" role="alert">{{ error }}</p>
  </form>
</template>

<style scoped>
.mcp-form { display: grid; grid-template-columns: minmax(0, 1fr); gap: 16px; min-width: 0; }
.mcp-input { width: 100%; min-width: 0; height: var(--settings-control-height, 36px); padding: 7px 10px; border: 1px solid var(--color-border); border-radius: 6px; color: var(--color-text); background: var(--color-inset); font-family: inherit; font-size: var(--settings-text-size, 13px); }
.mcp-input:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.mcp-form :deep(.form-field) { min-width: 0; }
.mcp-form__lower { grid-column: 1 / -1; display: flex; align-items: end; gap: 12px 16px; flex-wrap: wrap; }
.mcp-form__lower > :first-child { width: 160px; }
.mcp-permission { display: flex; align-items: center; gap: 8px; min-height: var(--settings-control-height, 36px); font-size: var(--settings-label-size, 13px); }
.mcp-permission input, .mcp-grant-toggles input { accent-color: var(--color-primary); width: 16px; height: 16px; flex-shrink: 0; }
.mcp-form__lower > :last-child { margin-inline-start: auto; height: var(--settings-action-height, 34px); }
.mcp-icon { width: 16px; height: 16px; }
.mcp-warning, .mcp-error { grid-column: 1 / -1; margin: 0; font-size: 12px; line-height: 1.65; overflow-wrap: anywhere; }
.mcp-warning { color: var(--color-dim); }
.mcp-error { color: var(--color-red); }
.mcp-config-grants { grid-column: 1 / -1; min-width: 0; margin: 0; padding: 12px 14px; border: 1px solid var(--color-border); border-radius: 7px; }
.mcp-config-grants legend { padding: 0 5px; color: var(--color-text); font-size: 13px; font-weight: 600; }
.mcp-grants-hint { margin: 0; color: var(--color-dim); font-size: 12px; line-height: 1.65; }
.mcp-project-grant { padding: 12px 0; border-bottom: 1px solid var(--color-border); }
.mcp-project-grant:last-of-type { border-bottom: 0; }
.mcp-project-grant > strong { display: block; overflow-wrap: anywhere; font-size: 13px; }
.mcp-grant-toggles { display: flex; flex-wrap: wrap; gap: 4px 16px; margin-top: 7px; }
.mcp-grant-toggles label { display: inline-flex; gap: 7px; align-items: center; min-height: 32px; font-size: 13px; cursor: pointer; }
.mcp-grant-ranges { display: grid; gap: 14px; padding-top: 8px; }
@media (min-width: 640px) { .mcp-form { grid-template-columns: minmax(180px, 1fr) minmax(200px, 2fr); } }
@media (min-width: 720px) { .mcp-grant-ranges { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); } }
@media (max-width: 639px) { .mcp-form__lower > :last-child { margin-inline-start: 0; min-height: 44px; } .mcp-permission, .mcp-grant-toggles label { min-height: 44px; } .mcp-form :deep(.n-base-selection-label) { min-height: 44px; } .mcp-form :deep(.n-base-selection-input) { font-size: 16px; } }
</style>
