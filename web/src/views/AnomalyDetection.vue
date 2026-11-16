<script setup lang="ts">
import { computed } from "vue";
import Content from "./AnomalyDetectionContent.vue";
import { useSessionStore } from "../stores/session";
import { usePermissionStore } from "../stores/permissions";
const session = useSessionStore(),
  permissions = usePermissionStore();
const ready = computed(
  () =>
    !!session.user &&
    !session.user.mustChangePassword &&
    !session.isNetworkError &&
    (session.user.multiTenant === false ||
      (session.user.multiTenant === true && !!permissions.activeTeam)),
);
const scopeKey = computed(() =>
  JSON.stringify([
    session.user,
    session.isNetworkError,
    permissions.activeTeam,
  ]),
);
</script>
<template><Content v-if="ready" :key="scopeKey" /></template>
