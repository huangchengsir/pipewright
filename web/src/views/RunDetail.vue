<script setup lang="ts">
import {computed} from 'vue'
import {useRoute} from 'vue-router'
import PermissionGate from '../components/PermissionGate.vue'
import IdentityPage from '../components/IdentityPage.vue'
import RunDetailContent from './RunDetailContent.vue'
const route=useRoute(),runId=computed(()=>String(route.params.id))
</script>
<template><PermissionGate :query="{action:'run.read',resourceId:runId}"><IdentityPage :component="RunDetailContent" :scope-key="runId" /></PermissionGate></template>
