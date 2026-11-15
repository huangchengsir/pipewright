<script setup lang="ts">
import {computed} from 'vue'
import PermissionGate from '../PermissionGate.vue'
import Content from './PromotionPanelContent.vue'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
const props=defineProps<{runId:string;projectId:string;runStatus:string}>()
const emit=defineEmits<{(e:'promotion-pending'):void}>()
const session=useSessionStore(),permissions=usePermissionStore()
const identity=computed(()=>JSON.stringify([props.runId,props.projectId,session.user,session.isNetworkError,permissions.activeTeam]))
</script>
<template><PermissionGate :query="{action:'run.read',resourceId:props.runId}"><Content :key="identity" v-bind="props" @promotion-pending="emit('promotion-pending')" /></PermissionGate></template>
