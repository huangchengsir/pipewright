<script setup lang="ts">
import { computed } from "vue";
import { useSessionStore } from "../../stores/session";
import { usePermissionStore } from "../../stores/permissions";
import Content from "./KubeReleaseSettingsContent.vue";
import type { KubeConnection } from "../../api/kubernetes";
import type { Project } from "../../api/projects";
const props = defineProps<{
  connection: KubeConnection;
  projects: Project[];
}>();
const session = useSessionStore(),
  permissions = usePermissionStore();
const key = computed(() =>
  JSON.stringify([
    session.user,
    session.isNetworkError,
    permissions.activeTeam,
    props.connection.id,
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
    v-bind="props"
  />
</template>
