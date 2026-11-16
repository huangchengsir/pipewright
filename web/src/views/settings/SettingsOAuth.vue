<script setup lang="ts">
import AuthorizedPage from '../../components/AuthorizedPage.vue'
import Content from './SettingsOAuthContent.vue'
</script>
<template><AuthorizedPage :component="Content" :query="{action:'system.manage',resourceId:'instance'}" /></template>
