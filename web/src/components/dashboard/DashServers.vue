<script setup lang="ts">
/**
 * DashServers — 概览页「服务器健康」(presentational)。
 * 每台机一张紧凑卡:可达性 + 内存/磁盘使用率条 + CPU 负载。不可达显错误态。
 */
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import type { Server, ServerMetrics } from '../../api/servers'

const props = defineProps<{
  servers: Server[]
  metrics: ServerMetrics[]
  loading: boolean
}>()

const router = useRouter()

interface Row {
  id: string
  name: string
  host: string
  reachable: boolean
  error: string
  load: string
  memPct: number | null
  memText: string
  diskPct: number | null
  diskText: string
}

const rows = computed<Row[]>(() =>
  props.servers.map((s) => {
    const m = props.metrics.find((x) => x.serverId === s.id)
    const mem = m?.memory
    const disk = m?.disk
    return {
      id: s.id,
      name: s.name,
      host: s.host,
      reachable: m?.reachable ?? false,
      error: m?.error ?? '',
      load: m?.cpu?.loadavg1 != null ? m.cpu.loadavg1.toFixed(2) : '—',
      memPct: mem && mem.totalBytes > 0 ? Math.round((mem.usedBytes / mem.totalBytes) * 100) : null,
      memText: mem ? `${fmtGB(mem.usedBytes)} / ${fmtGB(mem.totalBytes)}` : '—',
      diskPct: disk && disk.totalBytes > 0 ? Math.round((disk.usedBytes / disk.totalBytes) * 100) : null,
      diskText: disk ? `${fmtGB(disk.usedBytes)} / ${fmtGB(disk.totalBytes)}` : '—',
    }
  }),
)

function fmtGB(bytes: number): string {
  const gb = bytes / 1024 ** 3
  return gb >= 10 ? `${Math.round(gb)}G` : `${gb.toFixed(1)}G`
}

// 使用率 → 色调(<70 正常 / 70-85 偏高 / >85 危险)。
function tone(pct: number | null): string {
  if (pct === null) return 'na'
  if (pct >= 85) return 'err'
  if (pct >= 70) return 'warn'
  return 'ok'
}
</script>

<template>
  <section class="card srv" aria-labelledby="dash-srv-h">
    <header class="card-head">
      <h2 id="dash-srv-h" class="card-title">服务器健康</h2>
      <button class="card-link" type="button" @click="router.push('/server-status')">全部 →</button>
    </header>

    <div v-if="loading" class="srv-grid">
      <span v-for="i in 2" :key="i" class="sk-card" />
    </div>

    <p v-else-if="!rows.length" class="card-empty">尚未登记服务器。在「服务器」页添加目标机后,这里显示实时 CPU/内存/磁盘。</p>

    <div v-else class="srv-grid">
      <article v-for="r in rows" :key="r.id" class="srv-card" :class="{ down: !r.reachable }">
        <div class="srv-top">
          <span class="srv-name">{{ r.name }}</span>
          <span class="srv-state" :class="r.reachable ? 'up' : 'down'">
            <span class="srv-dot" aria-hidden="true" />
            {{ r.reachable ? '在线' : '离线' }}
          </span>
        </div>
        <div class="srv-host">{{ r.host }}</div>

        <template v-if="r.reachable">
          <div class="srv-metric">
            <div class="srv-metric-head"><span>内存</span><span class="srv-metric-val">{{ r.memPct ?? '—' }}%</span></div>
            <div class="srv-bar"><span class="srv-fill" :class="tone(r.memPct)" :style="{ width: (r.memPct ?? 0) + '%' }" /></div>
            <div class="srv-metric-sub">{{ r.memText }}</div>
          </div>
          <div class="srv-metric">
            <div class="srv-metric-head"><span>磁盘</span><span class="srv-metric-val">{{ r.diskPct ?? '—' }}%</span></div>
            <div class="srv-bar"><span class="srv-fill" :class="tone(r.diskPct)" :style="{ width: (r.diskPct ?? 0) + '%' }" /></div>
            <div class="srv-metric-sub">{{ r.diskText }}</div>
          </div>
          <div class="srv-load">负载 <strong>{{ r.load }}</strong></div>
        </template>
        <p v-else class="srv-err">{{ r.error || '无法连接' }}</p>
      </article>
    </div>
  </section>
</template>

<style scoped>
.srv-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(190px, 1fr));
  gap: 12px;
}
.srv-card {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 12px 14px;
  background: var(--color-surface);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.srv-card.down {
  background: var(--color-danger-soft);
  border-color: var(--color-red-line, var(--color-danger));
}
.srv-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.srv-name {
  font-size: var(--text-caption);
  font-weight: 700;
  color: var(--color-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.srv-state {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--text-micro);
  font-weight: 600;
  white-space: nowrap;
}
.srv-state.up {
  color: var(--color-success);
}
.srv-state.down {
  color: var(--color-danger);
}
.srv-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
.srv-state.up .srv-dot {
  animation: dash-blink 1.6s ease-in-out infinite;
}
.srv-host {
  font-size: var(--text-micro);
  color: var(--color-faint);
  font-family: var(--font-mono);
  margin-top: -4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.srv-metric-head {
  display: flex;
  justify-content: space-between;
  font-size: var(--text-micro);
  color: var(--color-text-soft);
}
.srv-metric-val {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  color: var(--color-text);
}
.srv-bar {
  height: 6px;
  border-radius: 999px;
  background: var(--color-inset);
  overflow: hidden;
  margin: 3px 0;
}
.srv-fill {
  display: block;
  height: 100%;
  border-radius: 999px;
  transition: width var(--duration-normal) var(--ease-out-expo, ease);
}
.srv-fill.ok {
  background: var(--color-green);
}
.srv-fill.warn {
  background: var(--color-amber);
}
.srv-fill.err {
  background: var(--color-red);
}
.srv-fill.na {
  background: var(--color-faint);
}
.srv-metric-sub {
  font-size: var(--text-micro);
  color: var(--color-faint);
  font-variant-numeric: tabular-nums;
}
.srv-load {
  font-size: var(--text-micro);
  color: var(--color-text-soft);
}
.srv-load strong {
  font-variant-numeric: tabular-nums;
  color: var(--color-text);
}
.srv-err {
  font-size: var(--text-micro);
  color: var(--color-danger);
  margin: 0;
}
.sk-card {
  height: 130px;
  border-radius: var(--radius-md);
  background: linear-gradient(90deg, var(--color-inset), var(--color-surface-hover), var(--color-inset));
  background-size: 200% 100%;
  animation: dash-shimmer 1.3s ease-in-out infinite;
}
@keyframes dash-shimmer {
  to {
    background-position: -200% 0;
  }
}
@keyframes dash-blink {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.35;
  }
}
</style>
