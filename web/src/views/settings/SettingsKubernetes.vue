<script setup lang="ts">
import { computed } from "vue";
import { useSessionStore } from "../../stores/session";
import { usePermissionStore } from "../../stores/permissions";
import Content from "./SettingsKubernetesContent.vue";
const session = useSessionStore(),
  permissions = usePermissionStore();
const key = computed(() =>
  JSON.stringify([
    session.user,
    session.isNetworkError,
    permissions.activeTeam,
  ]),
);
</script>
<template>
  <Content
    v-if="
      session.user &&
      !session.user.mustChangePassword &&
      !session.isNetworkError
    "
    :key="key"
  />
</template>
