<script setup lang="ts">
import AuthorizedPage from '../../components/AuthorizedPage.vue'
import Content from './SettingsDatabaseContent.vue'
defineProps<{recovery?:boolean}>()
</script>
<template>
 <Content v-if="recovery" recovery />
 <AuthorizedPage v-else :component="Content" :query="{action:'system.manage',resourceId:'instance'}" />
</template>
