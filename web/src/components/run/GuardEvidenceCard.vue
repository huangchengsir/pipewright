<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NIcon } from 'naive-ui'
import { Activity, AlertCircle, FileText } from '@vicons/tabler'
import type { GuardEvidence } from '../../api/releaseGuard'
import {
  guardDate,
  presentGuardEvidence,
} from '../../utils/guardEvidencePresentation'

const props = defineProps<{
  evidence: GuardEvidence
  targetName: string
  highlighted?: boolean
}>()
const { t, locale } = useI18n()
const presentation = computed(() =>
  presentGuardEvidence(props.evidence, t, locale.value),
)
const logExcerpt = computed(() =>
  props.evidence.kind === 'logs' &&
  typeof props.evidence.value?.text === 'string'
    ? props.evidence.value.text.slice(0, 600)
    : '',
)
</script>

<template>
  <article
    :id="`guard-evidence-${encodeURIComponent(evidence.id)}`"
    class="guard-evidence"
    :class="{ 'is-highlighted': highlighted }"
    :data-tone="presentation.tone"
    tabindex="-1"
    :aria-label="presentation.title"
  >
    <header>
      <span class="evidence-symbol" aria-hidden="true"
        ><NIcon :size="18"
          ><AlertCircle v-if="presentation.tone !== 'neutral'" /><FileText
            v-else-if="evidence.kind === 'logs'" /><Activity v-else /></NIcon
      ></span>
      <div class="evidence-heading">
        <h3>{{ presentation.title }}</h3>
        <p>{{ presentation.kind }} · {{ targetName }}</p>
      </div>
      <time
        :datetime="evidence.eventAt"
        :aria-label="t('releaseGuard.eventTime')"
        >{{ guardDate(evidence.eventAt, locale, t) }}</time
      >
    </header>
    <dl class="evidence-facts">
      <div v-for="fact in presentation.facts" :key="fact.label">
        <dt>{{ fact.label }}</dt>
        <dd>{{ fact.value }}</dd>
      </div>
    </dl>
    <p v-if="evidence.truncated" class="evidence-notice">
      {{ t('releaseGuard.truncated') }}
    </p>
    <p
      v-for="notice in presentation.notices"
      :key="notice"
      class="evidence-notice"
    >
      {{ notice }}
    </p>
    <div v-if="logExcerpt" class="evidence-log">
      <p>{{ t('releaseGuardPresentation.logExcerpt') }}</p>
      <pre>{{ logExcerpt }}</pre>
    </div>
    <details>
      <summary>{{ t('releaseGuardPresentation.technicalDetails') }}</summary>
      <dl class="evidence-meta">
        <dt>{{ t('releaseGuard.collectedTime') }}</dt>
        <dd>{{ guardDate(evidence.collectedAt, locale, t) }}</dd>
      </dl>
      <pre>{{ JSON.stringify(evidence, null, 2) }}</pre>
    </details>
  </article>
</template>

<style scoped>
.guard-evidence {
  padding: 20px 0;
  border-top: 1px solid var(--color-border);
  scroll-margin-top: 24px;
  min-width: 0;
  overflow-wrap: anywhere;
}
.guard-evidence.is-highlighted {
  background: var(--color-primary-soft);
  border-radius: var(--rounded);
  padding-inline: 14px;
}
header {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  flex-wrap: wrap;
}
.evidence-symbol {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: 50%;
  background: var(--color-card-2);
  color: var(--color-dim);
  flex-shrink: 0;
}
[data-tone='danger'] .evidence-symbol {
  background: var(--color-red-soft);
  color: var(--color-red);
}
[data-tone='warning'] .evidence-symbol {
  background: var(--color-amber-soft);
  color: var(--color-amber);
}
.evidence-heading {
  flex: 1 1 160px;
  min-width: 0;
}
h3 {
  font-size: 0.9rem;
  margin: 0 0 4px;
}
p,
time,
dt {
  font-size: 0.75rem;
  color: var(--color-dim);
  margin: 0;
  line-height: 1.7;
}
time {
  margin-left: 40px;
}
.evidence-facts {
  display: flex;
  gap: 10px 24px;
  flex-wrap: wrap;
  margin: 14px 0 4px 40px;
}
.evidence-facts > div {
  display: flex;
  gap: 8px;
  align-items: baseline;
  flex-wrap: wrap;
}
dd {
  font-size: 0.8rem;
  margin: 0;
}
details {
  margin-left: 40px;
}
summary {
  cursor: pointer;
  display: flex;
  align-items: center;
  min-height: 44px;
  color: var(--color-primary);
  font-size: 0.8rem;
}
.evidence-meta {
  margin: 0 0 8px;
}
.evidence-log {
  margin-top: 12px;
}
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 420px;
  overflow: auto;
  font: 0.75rem/1.7 var(--font-mono);
  background: var(--color-card-2);
  padding: 14px;
  border-radius: var(--rounded);
}
.evidence-notice {
  margin: 10px 0 0 40px;
  color: var(--color-amber);
}
summary:focus-visible,
.guard-evidence:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 3px;
}
@media (min-width: 700px) {
  time {
    margin-left: auto;
    max-width: 40%;
  }
}
@media (max-width: 480px) {
  .evidence-facts,
  details,
  .evidence-notice {
    margin-left: 0;
  }
}
@media (min-width: 640px) {
  :is(h3, h4) {
    font-size: 14px;
  }
  :is(p, label, select, li) {
    font-size: 13px;
  }
  :is(summary, label, select, a) {
    min-height: 34px;
  }
  :deep(.app-btn) {
    min-height: 34px;
    height: auto;
    padding-block: 6px;
    font-size: 13px;
    white-space: normal;
  }
}
@media (max-width: 639px) {
  :is(input:not([type='checkbox']):not([type='radio']), select, textarea) {
    font-size: 16px;
    min-height: 44px;
  }
}
</style>
