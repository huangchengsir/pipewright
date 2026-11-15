<script setup lang="ts">
import IdentityPage from '../components/IdentityPage.vue'
import Content from './MCPConfigReviewContent.vue'
import {useRoute} from 'vue-router'
const route=useRoute()
</script>
<template><IdentityPage :component="Content" :scope-key="String(route.params.id)" /></template>
