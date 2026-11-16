<script setup lang="ts">
import {computed} from 'vue'
import {useRoute} from 'vue-router'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import PermissionGate from '../components/PermissionGate.vue'
import ProjectPipelineContent from './ProjectPipelineContent.vue'
const route=useRoute(),session=useSessionStore(),permissions=usePermissionStore()
const id=computed(()=>String(route.params.id??''))
const identity=computed(()=>JSON.stringify([session.user,session.isNetworkError,permissions.activeTeam,id.value]))
</script>
<template><PermissionGate :query="{action:'pipeline.read',resourceId:id}"><ProjectPipelineContent :key="identity" /></PermissionGate></template>
