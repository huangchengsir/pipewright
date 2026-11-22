<script setup lang="ts">
import {computed} from 'vue'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import ProjectsContent from './ProjectsContent.vue'
import ProjectWorkspace from './ProjectWorkspace.vue'
import {useProjectModeStore} from '../stores/projectMode'
const session=useSessionStore(),permissions=usePermissionStore()
const projectMode=useProjectModeStore()
const canRender=computed(()=>session.user && !session.user.mustChangePassword && !session.isNetworkError)
const identity=computed(()=>JSON.stringify([session.user,session.isNetworkError,permissions.activeTeam]))
</script>
<template>
  <template v-if="canRender && !projectMode.loading">
    <ProjectWorkspace v-if="projectMode.mode === 'multi'" :key="identity" />
    <ProjectsContent v-else :key="identity" />
  </template>
</template>
