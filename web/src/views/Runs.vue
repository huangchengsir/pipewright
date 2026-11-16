<script setup lang="ts">
import IdentityPage from '../components/IdentityPage.vue'
import Content from './RunsContent.vue'
import {useRoute} from 'vue-router'
const route=useRoute()
</script>
<template><IdentityPage :component="Content" :scope-key="route.fullPath" /></template>
