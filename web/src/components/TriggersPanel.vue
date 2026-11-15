<script setup lang="ts">
import {computed,ref} from 'vue'
import PermissionGate from './PermissionGate.vue'
import Content from './TriggersPanelContent.vue'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import type {PipelineSaveContext} from '../composables/usePipelineDraftSave'
const props=defineProps<{projectId:string;disabled?:boolean}>()
const session=useSessionStore(),permissions=usePermissionStore()
const identity=computed(()=>JSON.stringify([props.projectId,session.user,session.isNetworkError,permissions.activeTeam]))
const content=ref<InstanceType<typeof Content>>()
defineExpose({ready:computed(()=>content.value?.ready??false),dirty:computed(()=>content.value?.dirty??false),saving:computed(()=>content.value?.saving??false),configRevision:computed(()=>content.value?.configRevision),validate:()=>content.value?.validate()??false,save:(context?:PipelineSaveContext)=>content.value?.save(context)??Promise.resolve(false),advanceConfigRevision:(from:string,to:string)=>content.value?.advanceConfigRevision(from,to),reload:()=>content.value?.reload()})
</script>
<template><PermissionGate :query="{action:'pipeline.read',resourceId:props.projectId}"><Content ref="content" :key="identity" :project-id="props.projectId" :disabled="props.disabled" /></PermissionGate></template>
