<script setup lang="ts">
import { computed } from "vue";
import PermissionGate from "../../components/PermissionGate.vue";
import SettingsNotificationsContent from "./SettingsNotificationsContent.vue";
import { useSessionStore } from "../../stores/session";
import { usePermissionStore } from "../../stores/permissions";
const session = useSessionStore(),
  permissions = usePermissionStore();
const scopeKey = computed(() =>
  JSON.stringify([
    session.user,
    session.isNetworkError,
    permissions.activeTeam,
  ]),
);
</script>
<template>
  <PermissionGate :query="{ action: 'system.manage', resourceId: 'instance' }">
    <SettingsNotificationsContent :key="scopeKey" />
  </PermissionGate>
</template>
