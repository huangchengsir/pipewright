<script setup lang="ts">
import {computed} from 'vue'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import LibraryContent from './LibraryContent.vue'
const session=useSessionStore(),permissions=usePermissionStore()
const identity=computed(()=>JSON.stringify([session.user,session.isNetworkError,permissions.activeTeam]))
</script>
<template><LibraryContent v-if="session.user && !session.user.mustChangePassword && !session.isNetworkError" :key="identity" /></template>
