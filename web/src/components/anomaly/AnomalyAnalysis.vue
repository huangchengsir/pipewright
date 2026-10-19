<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AnomalyAnalysis } from '../../api/anomaly'

const props = defineProps<{ analysis: AnomalyAnalysis | null }>()
const { t } = useI18n()
const statusKey = computed(() => {
  switch (props.analysis?.status) {
    case 'pending': return 'anomaly.analysisPending'
    case 'ready': return 'anomaly.analysisReady'
    default: return 'anomaly.analysisUnavailable'
  }
})
const generatedAt = computed(() => {
  const at = props.analysis?.generatedAt
  if (!at) return ''
  const date = new Date(at)
  return Number.isNaN(date.getTime()) ? at : date.toLocaleString()
})
</script>

<template>
  <section
    v-if="analysis"
    class="analysis"
    :class="`analysis--${analysis.status}`"
    :aria-label="t('anomaly.analysisHeading')"
    aria-live="polite"
    :aria-busy="analysis.status === 'pending'"
  >
    <header class="analysis__header">
      <h3 class="analysis__title">{{ t('anomaly.analysisHeading') }}</h3>
      <span class="analysis__status" role="status">{{ t(statusKey) }}</span>
      <time v-if="generatedAt" class="analysis__time" :datetime="analysis.generatedAt">
        {{ t('anomaly.analysisGeneratedAt', { at: generatedAt }) }}
      </time>
    </header>
    <p v-if="analysis.reason" class="analysis__text analysis__muted">{{ analysis.reason }}</p>
    <template v-if="analysis.status === 'ready'">
      <p class="analysis__text analysis__muted">{{ t('anomaly.analysisCaution') }}</p>
      <div v-if="analysis.hypothesis" class="analysis__section">
        <h4 class="analysis__label">{{ t('anomaly.analysisHypothesis') }}</h4>
        <p class="analysis__text">{{ analysis.hypothesis }}</p>
      </div>
      <p v-if="analysis.confidence" class="analysis__text">
        <strong>{{ t('anomaly.analysisConfidence') }}:</strong> {{ analysis.confidence }}
      </p>
      <div v-if="analysis.alternateCauses.length" class="analysis__section">
        <h4 class="analysis__label">{{ t('anomaly.analysisAlternateCauses') }}</h4>
        <ul class="analysis__list">
          <li v-for="(cause, index) in analysis.alternateCauses" :key="index">{{ cause }}</li>
        </ul>
      </div>
      <div v-if="analysis.fixSuggestions.length" class="analysis__section">
        <h4 class="analysis__label">{{ t('anomaly.analysisFixSuggestions') }}</h4>
        <ul class="analysis__list">
          <li v-for="(suggestion, index) in analysis.fixSuggestions" :key="index">{{ suggestion }}</li>
        </ul>
      </div>
      <div v-if="analysis.evidence.length" class="analysis__section">
        <h4 class="analysis__label">{{ t('anomaly.analysisEvidence') }}</h4>
        <ul class="analysis__evidence">
          <li v-for="(item, index) in analysis.evidence" :key="index" class="analysis__evidence-item">
            <span class="analysis__line">{{ t('anomaly.analysisLine', { n: item.line }) }}</span>
            <code class="analysis__evidence-text">{{ item.text }}</code>
          </li>
        </ul>
      </div>
    </template>
  </section>
</template>

<style scoped>
.analysis {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid var(--color-line);
  font-size: var(--text-body, 0.9rem);
  line-height: 1.5;
  color: var(--color-text);
  overflow-wrap: anywhere;
}
.analysis__header {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 12px;
}
.analysis__title,
.analysis__label,
.analysis__text {
  margin: 0;
  font-size: inherit;
}
.analysis__title,
.analysis__label {
  font-weight: 600;
}
.analysis__status,
.analysis__time,
.analysis__muted,
.analysis__line {
  color: var(--color-dim);
}
.analysis__status,
.analysis__time,
.analysis__line {
  font-size: var(--text-label, 0.8rem);
}
.analysis--ready .analysis__status {
  color: var(--color-success);
}
.analysis--unavailable .analysis__status {
  color: var(--color-warning);
}
.analysis__section {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.analysis__text,
.analysis__evidence-text {
  white-space: pre-wrap;
}
.analysis__list {
  margin: 0;
  padding-left: 20px;
}
.analysis__evidence {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.analysis__evidence-item {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 10px;
}
.analysis__evidence-text {
  flex: 1 1 240px;
  min-width: 0;
  font-size: var(--text-label, 0.8rem);
}
</style>
