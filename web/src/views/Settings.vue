<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import "../styles/settings-surface.css";

const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const groups = computed(() => [
  {
    label: t("kubernetes.groups.resources"),
    sections: [
      { path: "/settings/servers", label: t("settingsHub.navServers") },
      { path: "/settings/kubernetes", label: "Kubernetes" },
      {
        path: "/settings/dns-providers",
        label: t("settingsHub.navDnsProviders"),
      },
    ],
  },
  {
    label: t("kubernetes.groups.integrations"),
    sections: [
      { path: "/settings/ai", label: t("settingsHub.navAi") },
      { path: "/settings/oauth", label: t("settingsHub.navOauth") },
      { path: "/settings/mcp", label: "MCP" },
    ],
  },
  {
    label: t("kubernetes.groups.security"),
    sections: [
      { path: "/settings/account", label: t("settingsHub.navAccount") },
      { path: "/settings/vault", label: t("settingsHub.navVault") },
    ],
  },
  {
    label: t("kubernetes.groups.system"),
    sections: [
      { path: "/settings/system", label: t("settingsHub.navSystem") },
      {
        path: "/settings/notifications",
        label: t("settingsHub.navNotifications"),
      },
      {
        path: "/settings/diagnosis-stats",
        label: t("settingsHub.navDiagnosisStats"),
      },
    ],
  },
]);
const sections = computed(() =>
  groups.value.flatMap((group) => group.sections),
);
const selectedSection = computed(
  () =>
    sections.value.find(
      (section) =>
        router.resolve(section.path).matched.at(-1) === route.matched.at(-1),
    )?.path ?? "/settings/ai",
);
function navigate(event: Event): void {
  const control = event.target as HTMLSelectElement;
  const path = control.value;
  const restoreSelection = () => {
    control.value = selectedSection.value;
  };
  if (
    !sections.value.some((section) => section.path === path) ||
    path === route.path
  ) {
    restoreSelection();
    return;
  }
  // An existing page guard can reject navigation (for example, an unsaved draft).
  // Keep the native control aligned with the route that actually remains active.
  void router.push(path).then(restoreSelection, restoreSelection);
}
</script>

<template>
  <div class="settings-view">
    <header class="view-header">
      <h1 class="view-title">{{ t("settingsHub.title") }}</h1>
      <p class="view-sub">{{ t("settingsHub.subtitle") }}</p>
    </header>

    <select
      class="settings-nav-select"
      :aria-label="t('settingsHub.navAria')"
      :value="selectedSection"
      @change="navigate"
    >
      <optgroup v-for="group in groups" :key="group.label" :label="group.label">
        <option
          v-for="section in group.sections"
          :key="section.path"
          :value="section.path"
        >
          {{ section.label }}
        </option>
      </optgroup>
    </select>
    <div class="settings-layout">
      <nav class="settings-nav" :aria-label="t('settingsHub.navAria')">
        <section
          v-for="group in groups"
          :key="group.label"
          class="settings-nav-group"
        >
          <h2>{{ group.label }}</h2>
          <router-link
            v-for="section in group.sections"
            :key="section.path"
            :to="section.path"
            class="settings-nav-item"
            >{{ section.label }}</router-link
          >
        </section>
      </nav>
      <div class="settings-content"><router-view /></div>
    </div>
  </div>
</template>

<style scoped>
.settings-view {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.view-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-bottom: 18px;
}
.view-title {
  font-size: 21px;
  font-weight: 650;
  letter-spacing: -0.02em;
  color: var(--color-text);
}
.view-sub {
  font-size: 13px;
  color: var(--color-dim);
  line-height: 1.55;
}
.settings-nav-select {
  width: 100%;
  min-height: 44px;
  font: 16px var(--font-sans);
  color: var(--color-text);
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  padding: 8px 12px;
  margin-bottom: 20px;
}
.settings-nav-select:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}
.settings-nav {
  display: none;
}
.settings-content {
  flex: 1;
  min-width: 0;
}
@media (min-width: 768px) {
  .settings-nav-select {
    display: none;
  }
  .settings-layout {
    display: grid;
    grid-template-columns: 172px minmax(0, 1fr);
    gap: 28px;
    align-items: start;
  }
  .settings-nav {
    display: flex;
    flex-direction: column;
    gap: 20px;
    border-right: 1px solid var(--color-border);
    padding-right: 16px;
  }
  .settings-nav-group {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .settings-nav-group h2 {
    font-size: 11px;
    font-weight: 600;
    color: var(--color-faint);
    padding: 0 10px 6px;
  }
  .settings-nav-item {
    padding: 8px 10px;
    font-size: 13px;
    font-weight: 500;
    color: var(--color-dim);
    text-decoration: none;
    border-radius: var(--rounded);
    border-left: 2px solid transparent;
  }
  .settings-nav-item:hover {
    background: var(--color-inset);
    color: var(--color-text);
  }
  .settings-nav-item.router-link-active {
    background: var(--color-primary-soft);
    color: var(--color-primary);
    border-left-color: var(--color-primary);
    font-weight: 600;
  }
  .settings-nav-item:focus-visible {
    outline: 2px solid var(--color-primary);
    outline-offset: 2px;
  }
}
</style>
