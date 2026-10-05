<script setup lang="ts">
import { useRouter } from 'vue-router'
import OnboardingFlow from '../components/onboarding/OnboardingFlow.vue'
import { useOnboardingStatus, dismissOnboarding } from '../composables/useOnboarding'
const router = useRouter()
const { snapshot, loading, error, flow, refresh, select } = useOnboardingStatus()
function skip(): void { dismissOnboarding(); void router.push('/') }
function navigate(to: string): void {
  const resolved = router.resolve(to)
  if (resolved.name === 'project-pipeline' || /\/projects\/[^/]+\/pipeline$/.test(resolved.path)) {
    void router.push({ path: resolved.path, query: { ...resolved.query,
      ...(!['success', 'legacy'].includes(flow.value.kind) ? { onboardingGuide: '1' } : {}) }, hash: resolved.hash })
  } else void router.push(to)
}
</script>

<template>
  <OnboardingFlow :snapshot="snapshot" :loading="loading" :error="error" :flow="flow"
    @select="select" @retry="refresh" @skip="skip" @navigate="navigate" />
</template>
