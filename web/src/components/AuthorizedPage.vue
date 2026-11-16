<script setup lang="ts">
import {computed,type Component} from 'vue'
import PermissionGate from './PermissionGate.vue'
import type {PermissionQuery} from '../api/access'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
defineProps<{component:Component;query:PermissionQuery}>()
const session=useSessionStore(),permissions=usePermissionStore()
const identity=computed(()=>JSON.stringify([session.user,session.isNetworkError,permissions.activeTeam]))
</script>
<template><PermissionGate :query="query"><component :is="component" :key="identity" /></PermissionGate></template>
