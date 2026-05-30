<script setup lang="ts">
/**
 * Onboarding view (Story 1.7) — first-run guide shell.
 *
 * Loads onboarding status (frontend-derived) and renders OnboardingFlow.
 * Skipping persists localStorage(onboarding_dismissed) and falls to /overview.
 */
import { onMounted } from 'vue'
import OnboardingFlow from '../components/onboarding/OnboardingFlow.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import { useOnboardingStatus, dismissOnboarding } from '../composables/useOnboarding'

const { status, loading, error, refresh } = useOnboardingStatus()

onMounted(refresh)

function onSkip(): void {
  dismissOnboarding()
}
</script>

<template>
  <div class="onboarding-view">
    <div v-if="loading" class="ob-loading" aria-busy="true">
      <SkeletonBlock :height="120" />
      <SkeletonBlock :height="110" />
      <SkeletonBlock :height="200" />
    </div>
    <ErrorState
      v-else-if="error"
      title="无法加载引导"
      :description="error"
      @retry="refresh"
    />
    <OnboardingFlow v-else :status="status" @skip="onSkip" />
  </div>
</template>

<style scoped>
.onboarding-view {
  padding: 40px 32px 0;
  max-width: 1080px;
  margin: 0 auto;
}
.ob-loading {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 1080px;
  margin: 0 auto;
}
</style>
