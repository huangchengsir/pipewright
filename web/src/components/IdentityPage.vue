<script setup lang="ts">
import {computed,type Component} from 'vue'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
const props=defineProps<{component:Component;scopeKey?:string}>()
const session=useSessionStore(),permissions=usePermissionStore()
const key=computed(()=>JSON.stringify([session.user,permissions.activeTeam,session.isNetworkError,props.scopeKey]))
const ready=computed(()=>!!session.user&&!session.user.mustChangePassword&&!session.isNetworkError&&(session.user.multiTenant===false||!!permissions.activeTeam))
</script>
<template><component :is="component" v-if="ready" :key="key" /></template>
