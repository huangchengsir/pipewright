<script setup lang="ts">
import {useI18n} from 'vue-i18n'
defineProps<{state?:'ready'|'required';compact?:boolean}>()
const {t}=useI18n()
</script>
<template>
 <p v-if="state" class="automation-authorization" :class="{'is-required':state==='required','is-compact':compact}" role="status" :title="compact&&state==='required'?t('projectPanels.automationAuthorization.requiredHint'):undefined" :aria-label="compact&&state==='required'?t('projectPanels.automationAuthorization.requiredHint'):undefined">
  <span class="authorization-label">{{t(state==='ready'?'projectPanels.automationAuthorization.ready':'projectPanels.automationAuthorization.required')}}</span>
  <span v-if="state==='required'&&!compact" class="authorization-hint">{{t('projectPanels.automationAuthorization.requiredHint')}}</span>
 </p>
</template>
<style scoped>
.automation-authorization{display:flex;align-items:baseline;gap:6px 10px;flex-wrap:wrap;margin:8px 0 0;font-size:.75rem;line-height:1.45;color:var(--color-muted)}
.authorization-label{font-weight:600;flex:none}
.is-required .authorization-label{color:var(--color-warning,#946000)}
.authorization-hint{flex:1;min-width:180px}
.is-compact{margin:4px 0 0;font-size:.7rem}
</style>
