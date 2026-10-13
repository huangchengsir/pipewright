<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ArrowUpRight } from '@vicons/tabler'
import type { Project } from '../../api/projects'

defineProps<{ project?: Project; hasLegacyOverrides: boolean }>()
const { t } = useI18n()
</script>

<template>
  <section class="source-binding" :aria-label="t('pipelineJob.projectSourceBinding')">
    <h3>{{ t('pipelineJob.projectSourceBinding') }}</h3>
    <dl v-if="project">
      <dt>{{ t('pipelineJob.fieldRepoUrlLabel') }}</dt><dd class="source-binding__mono">{{ project.repoUrl }}</dd>
      <dt>{{ t('pipelineJob.fieldBranchLabel') }}</dt><dd>{{ project.defaultBranch }}<span class="source-binding__muted"> · {{ t('pipelineJob.sourceRunBranch') }}</span></dd>
      <dt>{{ t('pipelineJob.fieldCredentialIdLabel') }}</dt><dd>{{ project.credentialName || t('pipelineJob.credentialUnavailable') }}</dd>
    </dl>
    <p v-else>{{ t('pipelineJob.sourceProjectUnavailable') }}</p>
    <p v-if="hasLegacyOverrides" class="source-binding__warning">{{ t('pipelineJob.sourceLegacyOverrides') }}</p>
    <RouterLink v-if="project" :to="{ path: '/projects', query: { onboardingEdit: project.id } }">
      <ArrowUpRight aria-hidden="true" />{{ t('pipelineJob.sourceEditBinding') }}
    </RouterLink>
  </section>
</template>

<style scoped>
.source-binding { padding: 14px; border-bottom: 1px solid var(--color-border); }
h3 { font-size: 0.8rem; font-weight: 600; margin: 0 0 12px; }
dl { display: grid; grid-template-columns: minmax(65px, 80px) minmax(0, 1fr); gap: 9px; margin: 0; font-size: 0.78rem; line-height: 1.6; }
dt { color: var(--color-dim); } dd { margin: 0; overflow-wrap: anywhere; }
.source-binding__mono { font-family: var(--font-mono); }
.source-binding__muted, p { color: var(--color-dim); }
p { margin: 10px 0 0; font-size: 0.75rem; line-height: 1.6; }
.source-binding__warning { color: var(--color-amber); }
a { display: inline-flex; align-items: center; gap: 4px; margin-top: 12px; font-size: 0.77rem; color: var(--color-primary); text-decoration: none; }
a svg { width: 14px; height: 14px; }
a:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 3px; }
</style>
