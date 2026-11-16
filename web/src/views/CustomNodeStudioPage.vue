<script setup lang="ts">
import {computed} from 'vue'
import {useRoute} from 'vue-router'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import type {PermissionQuery} from '../api/access'
import PermissionGate from '../components/PermissionGate.vue'
import CustomNodeStudioContent from './CustomNodeStudioContent.vue'
const route=useRoute(),session=useSessionStore(),permissions=usePermissionStore()
const id=computed(()=>typeof route.params.id==='string'?route.params.id:'')
const queries=computed<PermissionQuery[]>(()=>id.value ? [{action:'custom_node.read',resourceId:id.value},{action:'custom_node.edit',resourceId:id.value}]:[{action:'custom_node.create',resourceId:'',create:true}])
const identity=computed(()=>JSON.stringify([session.user,session.isNetworkError,permissions.activeTeam,id.value]))
</script>
<template><PermissionGate :query="queries"><CustomNodeStudioContent :key="identity" /></PermissionGate></template>
