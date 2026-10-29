<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UpgradeState } from '../../api/version'
const props = defineProps<{ state: UpgradeState }>()
const { t } = useI18n()
const stages: Partial<Record<UpgradeState, number>> = {
  accepted: 0,
  checking: 0,
  downloading: 0,
  verifying: 1,
  extracting: 1,
  installing: 2,
  restarting: 3,
}
const current = computed(() => stages[props.state])
const steps = [
  { key: 'download', path: 'M8 6.5 10 5v10M7.5 15h5' },
  { key: 'verify', path: 'M6.5 7.5a3.5 2.5 0 0 1 7 0c0 2-2 3-7 7.5h7' },
  {
    key: 'install',
    offset: 'translate(-0.25 0)',
    path: 'M6.5 5h7l-4 5h1.5a3 2.5 0 0 1 0 5h-4',
  },
  { key: 'restart', path: 'M11.5 15V5L6.5 12h7' },
]
</script>
<template>
  <ol
    v-if="current !== undefined"
    class="upgrade-steps"
    :aria-label="t('settingsSystem.taskTitle')"
  >
    <li
      v-for="(step, index) in steps"
      :key="step.key"
      :data-stage="step.key"
      :class="{ 'is-current': index === current, 'is-done': index < current }"
      :aria-current="index === current ? 'step' : undefined"
    >
      <span class="upgrade-step-symbol" aria-hidden="true"
        ><svg
          viewBox="0 0 20 20"
          width="14"
          height="14"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path
            :transform="
              index < current
                ? 'translate(0 -0.25)'
                : 'offset' in step
                  ? step.offset
                  : undefined
            "
            :d="index < current ? 'M5 10l3.5 3.5L15 7' : step.path"
          /></svg
      ></span>
      <span>{{ t('settingsSystem.upgradeSteps.' + step.key) }}</span>
    </li>
  </ol>
</template>
<style scoped>
.upgrade-steps {
  list-style: none;
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  padding: 0;
  margin: 4px 0;
  gap: 8px;
}
.upgrade-steps li {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  position: relative;
  text-align: center;
  font-size: 12px;
  line-height: 1.5;
  color: var(--color-dim);
  min-width: 0;
  overflow-wrap: anywhere;
}
.upgrade-steps li:not(:last-child)::after {
  content: '';
  position: absolute;
  height: 1px;
  background: var(--color-border-strong);
  left: calc(50% + 18px);
  right: calc(-50% + 10px);
  top: 11.5px;
}
.upgrade-step-symbol {
  box-sizing: border-box;
  position: relative;
  z-index: 1;
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  border-radius: 50%;
  flex: none;
}
.upgrade-step-symbol svg {
  display: block;
}
.upgrade-steps li.is-current {
  color: var(--color-primary);
  font-weight: 600;
}
.is-current .upgrade-step-symbol {
  color: var(--color-primary);
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}
.is-done .upgrade-step-symbol {
  color: var(--color-green);
  border-color: var(--color-green-line);
  background: var(--color-green-soft);
}
@media (max-width: 639px) {
  .upgrade-steps {
    gap: 4px;
  }
  .upgrade-steps li {
    font-size: 12px;
  }
}
</style>
