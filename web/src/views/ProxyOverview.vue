<script setup lang="ts">
/*
  ProxyOverview.vue — 证书总览大盘(R2 / E2.4 · FR-10)。

  运维视角:跨所有主机、所有域名的反代证书集中成一张表 —— 状态、签发详情、到期一眼看清,
  「将到期」自动高亮。续期由 Caddy 自动完成,这张表只为让你「安心确认」。

  - 顶部摘要卡:总路由数 / 已签发 / 申请中+失败 / 最近一个到期。
  - 表格列:域名(+别名)、主机、上游、证书状态徽章(复用 R1 三态视觉)、证书详情(签发/有效期)、
    每行刷新 + 跳到对应主机。
  - 排序/分组:按主机 或 按到期排序。
  数据来自 GET /api/proxy/overview(只读聚合)。每行刷新调 refreshProxyRoute,回写该行。
*/
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { NIcon } from 'naive-ui'
import { Lock, LockOpen, AlertTriangle, Refresh, Server, ExternalLink, ListCheck } from '@vicons/tabler'
import {
  getProxyOverview,
  refreshProxyRoute,
  type ProxyRouteOverview,
  type CertStatus,
} from '../api/reverseProxy'
import { HttpError } from '../api/http'
import { useToast } from '../composables/useToast'
import EmptyState from '../components/ui/EmptyState.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'

const { t } = useI18n()
const router = useRouter()
const toast = useToast()

type LoadState = 'idle' | 'loading' | 'loaded' | 'error'
const loadState = ref<LoadState>('idle')
const loadError = ref('')
const rows = ref<ProxyRouteOverview[]>([])
const refreshing = ref(false)

async function load(): Promise<void> {
  loadState.value = rows.value.length === 0 ? 'loading' : 'loaded'
  refreshing.value = true
  loadError.value = ''
  try {
    rows.value = await getProxyOverview()
    loadState.value = 'loaded'
  } catch (err) {
    loadState.value = rows.value.length === 0 ? 'error' : 'loaded'
    loadError.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? t('proxyOverview.errLoad', { status: err.status }))
        : t('proxyOverview.errNetwork')
  } finally {
    refreshing.value = false
  }
}

onMounted(load)

// ─── 到期解析:certDetail 是人类可读串,尽力从中抽一个日期(ISO 或 yyyy-mm-dd / yyyy/mm/dd)。 ──
// 抽不到 → undefined(表格里显示「—」,排序时沉底)。仅 issued 行有意义。
function parseExpiry(r: ProxyRouteOverview): Date | undefined {
  if (r.certStatus !== 'issued') return undefined
  const s = r.certDetail || ''
  const iso = s.match(/\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2})?(?:Z|[+-]\d{2}:?\d{2})?)?/)
  if (iso) {
    const d = new Date(iso[0].replace(' ', 'T'))
    if (!Number.isNaN(d.getTime())) return d
  }
  const slash = s.match(/\d{4}\/\d{2}\/\d{2}/)
  if (slash) {
    const d = new Date(slash[0].replace(/\//g, '-'))
    if (!Number.isNaN(d.getTime())) return d
  }
  return undefined
}

const DAY_MS = 86_400_000
function daysLeft(d: Date): number {
  return Math.ceil((d.getTime() - Date.now()) / DAY_MS)
}
// 30 天内视为「将到期」高亮(Caddy 默认到期前 30 天续期)。
const EXPIRING_DAYS = 30

interface DerivedRow {
  route: ProxyRouteOverview
  expiry?: Date
  days?: number
  expiring: boolean
}
const derived = computed<DerivedRow[]>(() =>
  rows.value.map((route) => {
    const expiry = parseExpiry(route)
    const days = expiry ? daysLeft(expiry) : undefined
    return { route, expiry, days, expiring: days !== undefined && days <= EXPIRING_DAYS }
  }),
)

// ─── 摘要 ─────────────────────────────────────────────────────────────────────
const total = computed(() => rows.value.length)
const issuedCount = computed(() => rows.value.filter((r) => r.certStatus === 'issued').length)
const pendingCount = computed(() => rows.value.filter((r) => r.certStatus === 'pending').length)
const failedCount = computed(() => rows.value.filter((r) => r.certStatus === 'failed').length)
const expiringCount = computed(() => derived.value.filter((d) => d.expiring).length)

// 最近一个到期:有效期内、天数最小的那条。
const soonest = computed<DerivedRow | undefined>(() => {
  const withExpiry = derived.value.filter((d) => d.days !== undefined)
  if (withExpiry.length === 0) return undefined
  return withExpiry.reduce((a, b) => (a.days! <= b.days! ? a : b))
})

// ─── 排序 / 分组 ──────────────────────────────────────────────────────────────
type SortMode = 'host' | 'expiry'
const sortMode = ref<SortMode>('expiry')

const sorted = computed<DerivedRow[]>(() => {
  const list = [...derived.value]
  if (sortMode.value === 'host') {
    list.sort(
      (a, b) =>
        a.route.serverName.localeCompare(b.route.serverName) ||
        a.route.domain.localeCompare(b.route.domain),
    )
  } else {
    // 按到期升序:有到期日的在前(天数小的优先),其余(无到期 / 失败 / 申请中)沉底。
    list.sort((a, b) => {
      const av = a.days ?? Number.POSITIVE_INFINITY
      const bv = b.days ?? Number.POSITIVE_INFINITY
      return av - bv || a.route.domain.localeCompare(b.route.domain)
    })
  }
  return list
})

// 按主机分组(仅 host 排序模式下渲染分组头)。
interface HostGroup {
  serverId: string
  serverName: string
  rows: DerivedRow[]
}
const grouped = computed<HostGroup[]>(() => {
  const map = new Map<string, HostGroup>()
  for (const d of sorted.value) {
    const key = d.route.serverId
    let g = map.get(key)
    if (!g) {
      g = { serverId: key, serverName: d.route.serverName, rows: [] }
      map.set(key, g)
    }
    g.rows.push(d)
  }
  return [...map.values()]
})

// ─── 行操作:刷新证书 / 跳主机 ──────────────────────────────────────────────────
const busy = ref<Set<string>>(new Set())
function isBusy(id: string): boolean {
  return busy.value.has(id)
}
async function refreshRow(d: DerivedRow): Promise<void> {
  const r = d.route
  if (isBusy(r.id)) return
  busy.value = new Set(busy.value).add(r.id)
  try {
    const updated = await refreshProxyRoute(r.id)
    rows.value = rows.value.map((x) => (x.id === r.id ? { ...x, ...updated } : x))
    toast.success(t('proxyOverview.refreshing'), { detail: updated.domain })
  } catch (err) {
    toast.error(t('proxyOverview.refreshFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('proxyOverview.errReq', { status: err.status }))
          : t('proxyOverview.errNetwork'),
    })
  } finally {
    const next = new Set(busy.value)
    next.delete(r.id)
    busy.value = next
  }
}

function gotoHost(d: DerivedRow): void {
  // 跳容器页(每台主机一张卡有「域名/反代」tab)。serverId 经 query 供高亮。
  void router.push({ name: 'containers', query: { server: d.route.serverId } })
}

function statusLabel(s: CertStatus): string {
  if (s === 'issued') return t('proxyOverview.statusIssued')
  if (s === 'failed') return t('proxyOverview.statusFailed')
  return t('proxyOverview.statusPending')
}

function expiryText(d: DerivedRow): string {
  if (d.days === undefined) return '—'
  if (d.days < 0) return t('proxyOverview.expiredAgo', { n: Math.abs(d.days) })
  if (d.days === 0) return t('proxyOverview.expiresToday')
  return t('proxyOverview.daysLeft', { n: d.days })
}
</script>

<template>
  <div class="ovw">
    <header class="ovw__header">
      <div class="ovw__title-wrap">
        <span class="ovw__eyebrow">
          <NIcon :size="13"><ListCheck /></NIcon>
          {{ t('proxyOverview.eyebrow') }}
        </span>
        <h1 class="ovw__title">{{ t('proxyOverview.title') }}</h1>
        <p class="ovw__sub">{{ t('proxyOverview.subtitle') }}</p>
      </div>
      <button class="ovw__refresh" :disabled="refreshing" @click="load">
        <NIcon :size="14"><Refresh /></NIcon>
        {{ refreshing ? t('proxyOverview.refreshingAll') : t('common.refresh') }}
      </button>
    </header>

    <!-- 首屏骨架 -->
    <div v-if="loadState === 'loading'" class="ovw__skel" :aria-label="t('proxyOverview.loadingAria')" aria-busy="true">
      <div class="ovw__skel-cards">
        <SkeletonBlock v-for="n in 4" :key="n" :height="84" width="100%" />
      </div>
      <SkeletonBlock :height="240" width="100%" />
    </div>

    <ErrorState
      v-else-if="loadState === 'error'"
      :title="t('proxyOverview.errTitle')"
      :description="loadError"
      @retry="load"
    />

    <EmptyState
      v-else-if="total === 0"
      :title="t('proxyOverview.emptyTitle')"
      :description="t('proxyOverview.emptyDesc')"
    />

    <template v-else>
      <!-- 摘要卡 -->
      <section class="cards" :aria-label="t('proxyOverview.summaryAria')">
        <article class="card card--total">
          <span class="card__num">{{ total }}</span>
          <span class="card__label">{{ t('proxyOverview.cardTotal') }}</span>
        </article>
        <article class="card card--issued">
          <span class="card__num">{{ issuedCount }}</span>
          <span class="card__label">{{ t('proxyOverview.cardIssued') }}</span>
        </article>
        <article class="card card--warn">
          <span class="card__num">{{ pendingCount + failedCount }}</span>
          <span class="card__label">{{ t('proxyOverview.cardPendingFailed') }}</span>
          <span class="card__break">{{ t('proxyOverview.cardPendingFailedBreak', { pending: pendingCount, failed: failedCount }) }}</span>
        </article>
        <article class="card card--expiry" :class="{ 'card--expiry-hot': soonest && soonest.expiring }">
          <span v-if="soonest" class="card__num">
            {{ soonest.days! < 0 ? '!' : soonest.days }}<span v-if="soonest.days! >= 0" class="card__unit">{{ t('proxyOverview.dayUnit') }}</span>
          </span>
          <span v-else class="card__num card__num--none">—</span>
          <span class="card__label">{{ t('proxyOverview.cardSoonest') }}</span>
          <span v-if="soonest" class="card__break mono" :title="soonest.route.domain">{{ soonest.route.domain }}</span>
          <span v-else class="card__break">{{ t('proxyOverview.cardSoonestNone') }}</span>
        </article>
      </section>

      <!-- 控件:排序 / 分组 + 将到期计数 -->
      <div class="ovw__controls">
        <div class="seg" role="tablist" :aria-label="t('proxyOverview.sortAria')">
          <button
            class="seg__btn"
            :class="{ 'seg__btn--on': sortMode === 'expiry' }"
            role="tab"
            :aria-selected="sortMode === 'expiry'"
            @click="sortMode = 'expiry'"
          >
            {{ t('proxyOverview.sortExpiry') }}
          </button>
          <button
            class="seg__btn"
            :class="{ 'seg__btn--on': sortMode === 'host' }"
            role="tab"
            :aria-selected="sortMode === 'host'"
            @click="sortMode = 'host'"
          >
            {{ t('proxyOverview.sortHost') }}
          </button>
        </div>
        <span v-if="expiringCount > 0" class="ovw__expiring">
          <NIcon :size="14"><AlertTriangle /></NIcon>
          {{ t('proxyOverview.expiringSoon', { n: expiringCount }) }}
        </span>
      </div>

      <!-- 表格 -->
      <div class="tablewrap">
        <table class="tbl">
          <thead>
            <tr>
              <th>{{ t('proxyOverview.colDomain') }}</th>
              <th>{{ t('proxyOverview.colHost') }}</th>
              <th>{{ t('proxyOverview.colUpstream') }}</th>
              <th>{{ t('proxyOverview.colStatus') }}</th>
              <th>{{ t('proxyOverview.colCertDetail') }}</th>
              <th>{{ t('proxyOverview.colExpiry') }}</th>
              <th class="tbl__act-h">{{ t('proxyOverview.colActions') }}</th>
            </tr>
          </thead>

          <!-- 按主机分组 -->
          <template v-if="sortMode === 'host'">
            <tbody v-for="g in grouped" :key="g.serverId">
              <tr class="grouprow">
                <td colspan="7">
                  <span class="grouprow__ic"><NIcon :size="13"><Server /></NIcon></span>
                  <span class="grouprow__name">{{ g.serverName }}</span>
                  <span class="grouprow__count">{{ t('proxyOverview.groupCount', { n: g.rows.length }) }}</span>
                </td>
              </tr>
              <tr
                v-for="d in g.rows"
                :key="d.route.id"
                class="datarow"
                :class="{ 'datarow--busy': isBusy(d.route.id), 'datarow--expiring': d.expiring, 'datarow--off': !d.route.enabled }"
              >
                <td class="cell-domain">
                  <div class="dom mono">{{ d.route.domain }}</div>
                  <div v-if="d.route.config.aliases.length" class="dom__aliases">
                    <span v-for="a in d.route.config.aliases" :key="a" class="dom__alias mono">{{ a }}</span>
                  </div>
                  <span v-if="!d.route.enabled" class="dom__off">{{ t('proxyOverview.disabled') }}</span>
                </td>
                <td class="cell-host">{{ d.route.serverName }}</td>
                <td class="cell-up mono">{{ d.route.upstreamContainer }}:{{ d.route.upstreamPort }}</td>
                <td>
                  <span class="badge" :class="`badge--${d.route.certStatus}`">
                    <NIcon :size="13">
                      <Lock v-if="d.route.certStatus === 'issued'" />
                      <AlertTriangle v-else-if="d.route.certStatus === 'failed'" />
                      <LockOpen v-else />
                    </NIcon>
                    {{ statusLabel(d.route.certStatus) }}
                  </span>
                </td>
                <td class="cell-detail mono" :title="d.route.certDetail">{{ d.route.certDetail || '—' }}</td>
                <td class="cell-expiry" :class="{ 'cell-expiry--hot': d.expiring }">{{ expiryText(d) }}</td>
                <td class="cell-act">
                  <button class="rowbtn" :disabled="isBusy(d.route.id)" :title="t('proxyOverview.refreshRowTitle')" @click="refreshRow(d)">
                    <NIcon :size="14"><Refresh /></NIcon>
                  </button>
                  <button class="rowbtn" :title="t('proxyOverview.gotoHostTitle')" @click="gotoHost(d)">
                    <NIcon :size="14"><ExternalLink /></NIcon>
                  </button>
                </td>
              </tr>
            </tbody>
          </template>

          <!-- 按到期排序(扁平) -->
          <tbody v-else>
            <tr
              v-for="d in sorted"
              :key="d.route.id"
              class="datarow"
              :class="{ 'datarow--busy': isBusy(d.route.id), 'datarow--expiring': d.expiring, 'datarow--off': !d.route.enabled }"
            >
              <td class="cell-domain">
                <div class="dom mono">{{ d.route.domain }}</div>
                <div v-if="d.route.config.aliases.length" class="dom__aliases">
                  <span v-for="a in d.route.config.aliases" :key="a" class="dom__alias mono">{{ a }}</span>
                </div>
                <span v-if="!d.route.enabled" class="dom__off">{{ t('proxyOverview.disabled') }}</span>
              </td>
              <td class="cell-host">{{ d.route.serverName }}</td>
              <td class="cell-up mono">{{ d.route.upstreamContainer }}:{{ d.route.upstreamPort }}</td>
              <td>
                <span class="badge" :class="`badge--${d.route.certStatus}`">
                  <NIcon :size="13">
                    <Lock v-if="d.route.certStatus === 'issued'" />
                    <AlertTriangle v-else-if="d.route.certStatus === 'failed'" />
                    <LockOpen v-else />
                  </NIcon>
                  {{ statusLabel(d.route.certStatus) }}
                </span>
              </td>
              <td class="cell-detail mono" :title="d.route.certDetail">{{ d.route.certDetail || '—' }}</td>
              <td class="cell-expiry" :class="{ 'cell-expiry--hot': d.expiring }">{{ expiryText(d) }}</td>
              <td class="cell-act">
                <button class="rowbtn" :disabled="isBusy(d.route.id)" :title="t('proxyOverview.refreshRowTitle')" @click="refreshRow(d)">
                  <NIcon :size="14"><Refresh /></NIcon>
                </button>
                <button class="rowbtn" :title="t('proxyOverview.gotoHostTitle')" @click="gotoHost(d)">
                  <NIcon :size="14"><ExternalLink /></NIcon>
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <p class="ovw__foot">
        <NIcon :size="13" class="ovw__foot-ic"><Lock /></NIcon>
        {{ t('proxyOverview.footNote') }}
      </p>
    </template>
  </div>
</template>

<style scoped>
.ovw {
  padding: 28px clamp(16px, 4vw, 40px) 40px;
  max-width: 1180px;
  margin: 0 auto;
}

/* 头部 */
.ovw__header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 18px;
  flex-wrap: wrap;
  margin-bottom: 22px;
}
.ovw__eyebrow {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-micro);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.09em;
  color: var(--color-primary);
}
.ovw__title {
  margin: 6px 0 4px;
  font-size: var(--text-display);
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--color-text);
}
.ovw__sub {
  margin: 0;
  font-size: var(--text-label);
  color: var(--color-dim);
  max-width: 62ch;
  line-height: 1.55;
}
.ovw__refresh {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  flex-shrink: 0;
  font-size: var(--text-label);
  font-weight: 600;
  padding: 8px 15px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-dim);
  cursor: pointer;
  transition:
    color var(--duration-fast, 150ms) ease,
    border-color var(--duration-fast, 150ms) ease;
}
.ovw__refresh:hover:not(:disabled) {
  color: var(--color-text);
  border-color: var(--color-text);
}
.ovw__refresh:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* 骨架 */
.ovw__skel {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.ovw__skel-cards {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 14px;
}

/* 摘要卡 —— bento 式,左侧色条标识语义 */
.cards {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 14px;
  margin-bottom: 24px;
}
.card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 18px 18px 16px 22px;
  border-radius: var(--rounded-card);
  border: 1px solid var(--color-border);
  background: var(--color-card);
  box-shadow: var(--shadow);
  overflow: hidden;
}
.card::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 4px;
  background: var(--color-faint);
}
.card--total::before {
  background: var(--color-primary);
}
.card--issued::before {
  background: var(--color-green);
}
.card--warn::before {
  background: var(--color-amber);
}
.card--expiry::before {
  background: var(--color-cyan);
}
.card--expiry-hot::before {
  background: var(--color-red);
}
.card--expiry-hot {
  border-color: var(--color-red-line);
}
.card__num {
  font-size: var(--text-kpi);
  font-weight: 700;
  line-height: 1.05;
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
}
.card__num--none {
  color: var(--color-faint);
}
.card__unit {
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-dim);
  margin-left: 2px;
}
.card--issued .card__num {
  color: var(--color-green);
}
.card--warn .card__num {
  color: var(--color-amber);
}
.card--expiry-hot .card__num {
  color: var(--color-red);
}
.card__label {
  font-size: var(--text-micro);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--color-faint);
}
.card__break {
  margin-top: 3px;
  font-size: var(--text-micro);
  color: var(--color-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 控件 */
.ovw__controls {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
  margin-bottom: 14px;
}
.seg {
  display: inline-flex;
  gap: 2px;
  padding: 3px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border);
  background: var(--color-inset);
}
.seg__btn {
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 6px 14px;
  border-radius: var(--rounded-sm);
  border: none;
  background: transparent;
  color: var(--color-faint);
  cursor: pointer;
  transition:
    color var(--duration-fast, 150ms) ease,
    background var(--duration-fast, 150ms) ease;
}
.seg__btn:hover {
  color: var(--color-dim);
}
.seg__btn--on {
  color: var(--color-text);
  background: var(--color-card);
  box-shadow: var(--shadow);
}
.ovw__expiring {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 5px 12px;
  border-radius: var(--rounded-full);
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
}

/* 表格 */
.tablewrap {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  background: var(--color-card);
  box-shadow: var(--shadow);
  overflow: hidden;
}
.tbl {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--text-label);
}
.tbl thead th {
  text-align: left;
  font-size: var(--text-micro);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--color-faint);
  padding: 11px 16px;
  background: var(--color-card-2);
  border-bottom: 1px solid var(--color-border);
  white-space: nowrap;
}
.tbl__act-h {
  text-align: right;
}
.datarow {
  border-bottom: 1px solid var(--color-border);
  transition: background var(--duration-fast, 150ms) ease;
}
.datarow:last-child {
  border-bottom: none;
}
.datarow:hover {
  background: var(--color-card-2);
}
.datarow--busy {
  opacity: 0.55;
}
.datarow--off {
  opacity: 0.7;
}
.datarow--expiring {
  background: var(--color-amber-soft);
}
.datarow--expiring:hover {
  background: var(--color-amber-soft);
}
.tbl td {
  padding: 12px 16px;
  vertical-align: top;
  color: var(--color-dim);
}
.cell-domain {
  min-width: 180px;
}
.dom {
  font-weight: 600;
  color: var(--color-text);
  word-break: break-all;
}
.dom__aliases {
  margin-top: 4px;
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.dom__alias {
  font-size: var(--text-micro);
  padding: 1px 7px;
  border-radius: var(--rounded-full);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.dom__off {
  display: inline-block;
  margin-top: 5px;
  font-size: var(--text-micro);
  padding: 1px 7px;
  border-radius: var(--rounded-full);
  background: var(--color-inset);
  color: var(--color-faint);
}
.cell-host {
  white-space: nowrap;
  color: var(--color-text);
}
.cell-up {
  font-size: var(--text-micro);
  color: var(--color-dim);
  word-break: break-all;
}
.cell-detail {
  font-size: var(--text-micro);
  color: var(--color-faint);
  max-width: 240px;
}
.cell-expiry {
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
  color: var(--color-dim);
}
.cell-expiry--hot {
  color: var(--color-amber);
  font-weight: 600;
}
.badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 4px 10px;
  border-radius: var(--rounded-full);
  white-space: nowrap;
}
.badge--issued {
  color: var(--color-green);
  background: var(--color-green-soft);
}
.badge--pending {
  color: var(--color-amber);
  background: var(--color-amber-soft);
}
.badge--failed {
  color: var(--color-red);
  background: var(--color-red-soft);
}
.cell-act {
  text-align: right;
  white-space: nowrap;
}
.rowbtn {
  display: inline-grid;
  place-items: center;
  width: 30px;
  height: 30px;
  margin-left: 4px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border);
  background: var(--color-card-2);
  color: var(--color-faint);
  cursor: pointer;
  transition:
    color var(--duration-fast, 150ms) ease,
    border-color var(--duration-fast, 150ms) ease;
}
.rowbtn:hover:not(:disabled) {
  color: var(--color-primary);
  border-color: var(--color-primary);
}
.rowbtn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

/* 分组头 */
.grouprow td {
  padding: 9px 16px;
  background: var(--color-inset);
  border-bottom: 1px solid var(--color-border);
}
.grouprow__ic {
  display: inline-flex;
  vertical-align: middle;
  color: var(--color-faint);
  margin-right: 7px;
}
.grouprow__name {
  font-size: var(--text-micro);
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--color-dim);
}
.grouprow__count {
  margin-left: 9px;
  font-size: var(--text-micro);
  color: var(--color-faint);
}

.ovw__foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 16px 2px 0;
  font-size: var(--text-micro);
  color: var(--color-faint);
  line-height: 1.5;
}
.ovw__foot-ic {
  color: var(--color-green);
  flex-shrink: 0;
}

@media (max-width: 880px) {
  .cards,
  .ovw__skel-cards {
    grid-template-columns: repeat(2, 1fr);
  }
  .tablewrap {
    overflow-x: auto;
  }
  .tbl {
    min-width: 760px;
  }
}
</style>
