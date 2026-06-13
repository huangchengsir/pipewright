<script setup lang="ts">
/**
 * ProjectTriggers — backward-compatible standalone page at /projects/:id/triggers.
 * The trigger form logic has been extracted to TriggersPanel.vue (Story 2-2).
 * This wrapper preserves the original route for existing links/bookmarks.
 */
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import TriggersPanel from '../components/TriggersPanel.vue'

const route = useRoute()
const { t } = useI18n()
const projectId = computed(() => route.params.id as string)
</script>

<template>
  <div class="triggers-root">
    <!-- ─── Page header ─────────────────────────────────────────────────── -->
    <header class="page-header">
      <div class="page-header-text">
        <nav class="breadcrumb" :aria-label="t('projectTriggers.breadcrumbAria')">
          <router-link to="/projects" class="crumb-link">{{ t('projectTriggers.breadcrumbProjects') }}</router-link>
          <span class="crumb-sep" aria-hidden="true">/</span>
          <span class="crumb-cur">{{ t('projectTriggers.title') }}</span>
        </nav>
        <h1 class="page-title">{{ t('projectTriggers.title') }}</h1>
        <p class="page-sub">{{ t('projectTriggers.subtitle') }}</p>
      </div>
    </header>

    <TriggersPanel :project-id="projectId" />
  </div>
</template>

<style scoped>
.triggers-root {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
}

.page-header {
  display: flex;
  align-items: flex-start;
}

.page-header-text {
  flex: 1;
}

.breadcrumb {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.76rem;
  color: var(--color-faint);
  margin-bottom: 6px;
}

.crumb-link {
  color: var(--color-dim);
  text-decoration: none;
  transition: color var(--duration-fast);
}

.crumb-link:hover {
  color: var(--color-primary);
}

.crumb-link:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
  border-radius: 2px;
}

.crumb-sep {
  color: var(--color-faint);
}

.crumb-cur {
  color: var(--color-text);
  font-weight: 500;
}

.page-title {
  font-size: 1.5rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--color-text);
  line-height: 1.2;
}

.page-sub {
  font-size: 0.82rem;
  color: var(--color-faint);
  margin-top: 5px;
  line-height: 1.5;
}
</style>
