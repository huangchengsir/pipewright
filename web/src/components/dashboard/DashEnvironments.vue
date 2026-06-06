<script setup lang="ts">
/**
 * DashEnvironments — 概览页「环境部署态」(presentational)。
 * 跨项目聚合每个环境的当前激活部署(最近一次全成功),显示项目·环境·commit·分支·时间。
 */
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import type { EnvDeployment } from '../../api/environments'

export interface DashEnvEntry {
  projectId: string
  projectName: string
  environment: string
  active: EnvDeployment | null
}

const props = defineProps<{
  entries: DashEnvEntry[]
  loading: boolean
}>()

const router = useRouter()

const rows = computed(() => props.entries.slice(0, 8))

function fmtAgo(rfc: string): string {
  const t = new Date(rfc).getTime()
  if (Number.isNaN(t)) return ''
  const m = Math.floor(Math.max(0, Date.now() - t) / 60000)
  if (m < 60) return `${m}m前`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h前`
  return `${Math.floor(h / 24)}d前`
}
</script>

<template>
  <section class="card env" aria-labelledby="dash-env-h">
    <header class="card-head">
      <h2 id="dash-env-h" class="card-title">环境部署态</h2>
      <button class="card-link" type="button" @click="router.push('/environments')">全部 →</button>
    </header>

    <div v-if="loading" class="env-skeleton" aria-busy="true">
      <span v-for="i in 4" :key="i" class="sk-row" />
    </div>

    <p v-else-if="!rows.length" class="card-empty">尚无环境部署。配置「分支→环境」映射并完成部署后,各环境当前版本会显示在此。</p>

    <ul v-else class="env-list" role="list">
      <li v-for="e in rows" :key="e.projectId + e.environment" class="env-row">
        <div class="env-id">
          <span class="env-name">{{ e.environment }}</span>
          <span class="env-proj">{{ e.projectName }}</span>
        </div>
        <template v-if="e.active">
          <code class="env-commit">{{ e.active.commit.slice(0, 7) }}</code>
          <span class="env-branch">{{ e.active.branch }}</span>
          <span class="env-time">{{ fmtAgo(e.active.deployedAt) }}</span>
          <span class="env-pill ok">已部署</span>
        </template>
        <span v-else class="env-pill none">未部署</span>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.env-list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.env-row {
  display: grid;
  grid-template-columns: 1.4fr auto auto auto auto;
  align-items: center;
  gap: 10px;
  padding: 9px 4px;
  border-bottom: 1px solid var(--color-border);
}
.env-row:last-child {
  border-bottom: none;
}
.env-id {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}
.env-name {
  font-size: var(--text-caption);
  font-weight: 700;
  color: var(--color-text);
  text-transform: capitalize;
}
.env-proj {
  font-size: var(--text-micro);
  color: var(--color-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.env-commit {
  font-family: var(--font-mono);
  font-size: var(--text-micro);
  color: var(--color-primary);
  background: var(--color-primary-soft);
  padding: 1px 6px;
  border-radius: 5px;
}
.env-branch {
  font-size: var(--text-micro);
  color: var(--color-text-soft);
  font-family: var(--font-mono);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 110px;
}
.env-time {
  font-size: var(--text-micro);
  color: var(--color-faint);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.env-pill {
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 999px;
  white-space: nowrap;
  justify-self: end;
}
.env-pill.ok {
  color: var(--color-success);
  background: var(--color-green-soft);
}
.env-pill.none {
  color: var(--color-faint);
  background: var(--color-inset);
  grid-column: 2 / -1;
}
.env-skeleton {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 0;
}
.sk-row {
  height: 34px;
  border-radius: var(--radius-sm);
  background: linear-gradient(90deg, var(--color-inset), var(--color-surface-hover), var(--color-inset));
  background-size: 200% 100%;
  animation: dash-shimmer 1.3s ease-in-out infinite;
}
@keyframes dash-shimmer {
  to {
    background-position: -200% 0;
  }
}
@media (max-width: 720px) {
  .env-row {
    grid-template-columns: 1fr auto auto;
  }
  .env-branch,
  .env-time {
    display: none;
  }
}
</style>
