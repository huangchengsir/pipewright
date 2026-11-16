<script setup lang="ts">
import {computed,ref} from 'vue'
import PermissionGate from './PermissionGate.vue'
import Content from './ChainPanelContent.vue'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
const props=defineProps<{projectId:string;disabled?:boolean}>()
const session=useSessionStore(),permissions=usePermissionStore()
const identity=computed(()=>JSON.stringify([props.projectId,session.user,session.isNetworkError,permissions.activeTeam]))
</script>
<template><PermissionGate :query="{action:'pipeline.read',resourceId:props.projectId}"><Content ref="content" :key="identity" :project-id="props.projectId" :disabled="props.disabled" /></PermissionGate></template>
