<script setup lang="ts">
/**
 * Overview (Story 1.7) — first-screen.
 *
 * This story ships the EMPTY state with a guiding CTA (real overview data = later stories):
 *   - no project yet → onboarding CTA + value reminder
 *   - has project    → neutral empty state ("概览数据将在后续 story 填充")
 */
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import AppButton from '../components/ui/AppButton.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'
import { useOnboardingStatus } from '../composables/useOnboarding'

const router = useRouter()
const { status, loading, refresh } = useOnboardingStatus()

onMounted(refresh)

function goOnboarding(): void {
  router.push('/onboarding')
}
function goProjects(): void {
  router.push('/projects')
}
</script>

<template>
  <div class="overview-view">
    <header class="view-header">
      <h1 class="view-title">概览</h1>
      <p class="view-sub">跨项目与服务器的实时状态首屏</p>
    </header>

    <div v-if="loading" class="ov-loading" aria-busy="true">
      <SkeletonBlock :height="220" />
    </div>

    <!-- No project yet: onboarding CTA -->
    <EmptyState
      v-else-if="!status.hasProject"
      title="还没有项目"
      description="跟随三步引导,连接 AI、添加服务器、创建第一个项目,即可触发首次部署。"
      icon-path="M13 2 3 14h7l-1 8 10-12h-7z"
    >
      <template #cta>
        <div class="ov-cta">
          <AppButton variant="primary" @click="goOnboarding">开始引导 →</AppButton>
          <AppButton variant="ghost" @click="goProjects">直接创建项目</AppButton>
        </div>
      </template>
    </EmptyState>

    <!-- Has project: overview data placeholder -->
    <EmptyState
      v-else
      title="概览数据将在后续 story 填充"
      description="项目与运行状态接入后,这里会展示跨项目的实时部署态势。"
    >
      <template #cta>
        <AppButton variant="default" @click="goProjects">查看项目</AppButton>
      </template>
    </EmptyState>
  </div>
</template>

<style scoped>
.overview-view {
  display: flex;
  flex-direction: column;
  gap: 24px;
}
.view-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-bottom: 20px;
  border-bottom: 1px solid var(--color-border);
}
.view-title {
  font-size: var(--text-display);
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--color-text);
}
.view-sub {
  font-size: var(--text-body);
  color: var(--color-faint);
  margin-top: 2px;
}
.ov-loading {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.ov-cta {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  justify-content: center;
}
</style>
