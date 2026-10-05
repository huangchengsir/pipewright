<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ArrowRight } from '@vicons/tabler'
import { resetOnboarding, useOnboardingPreferences } from '../../composables/useOnboarding'
withDefaults(defineProps<{ currentPath?: string }>(), { currentPath: '' })
const { preferences, continuing } = useOnboardingPreferences()
const { t } = useI18n()
function resumeOnboarding(): void {
  if (preferences.dismissed) resetOnboarding()
}
</script>

<template>
  <router-link v-if="!preferences.completed && (continuing || preferences.dismissed) && currentPath !== '/onboarding'" to="/onboarding" class="continue-onboarding"
    @click.capture="resumeOnboarding"
    data-testid="onboarding-continue" :title="t('onboarding.continue')">
    <span>{{ t('onboarding.continue') }}</span><ArrowRight aria-hidden="true" />
  </router-link>
</template>

<style scoped>
.continue-onboarding { position: fixed; right: 20px; bottom: 64px; z-index: 40; display: inline-flex; align-items: center; gap: 8px; height: 44px; max-width: calc(100vw - var(--rail-width) - 40px); padding: 0 12px; border: 1px solid var(--color-border); border-radius: var(--rounded); background: var(--color-card); box-shadow: var(--shadow); font-size: 14px; color: var(--color-primary); text-decoration: none; letter-spacing: 0; }
.continue-onboarding span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.continue-onboarding:hover { border-color: var(--color-primary); }
.continue-onboarding svg { width: 16px; height: 16px; flex: none; }
.continue-onboarding:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 3px; }
</style>
