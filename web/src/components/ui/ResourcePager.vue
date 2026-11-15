<script setup lang="ts">
import {useI18n} from 'vue-i18n'
import AppButton from './AppButton.vue'
defineProps<{page:number;pages:number;disabled?:boolean}>()
const emit=defineEmits<{change:[page:number]}>()
const {t}=useI18n()
</script>
<template><nav v-if="pages>1" class="resource-pager"><AppButton :disabled="disabled||page<=1" @click="emit('change',page-1)">{{t('runExperience.previous')}}</AppButton><span aria-live="polite">{{t('runExperience.pageOf',{page,total:pages})}}</span><AppButton :disabled="disabled||page>=pages" @click="emit('change',page+1)">{{t('runExperience.next')}}</AppButton></nav></template>
<style scoped>.resource-pager{display:flex;align-items:center;justify-content:flex-end;flex-wrap:wrap;gap:8px;margin:10px 0;color:var(--color-dim);font-size:12px}.resource-pager :deep(.app-btn){min-height:32px;height:32px}</style>
