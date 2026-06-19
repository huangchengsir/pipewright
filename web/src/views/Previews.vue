<script setup lang="ts">
/*
  Previews.vue — 「PR 预览环境」大盘(R4 / E4.1 · the headline)。

  每个 PR 一个临时环境,活在 pr-N-<proj>.<根域>:DNS + 证书 + 一次部署的容器。对标
  Vercel / Netlify 的预览部署,自托管版。这张板让你一眼看清某项目当前所有临时环境:
  PR 号 / 分支、活链接、状态(活跃 / 已回收)、创建时间,并可手动「回收」拆除。

  - 项目选择器(URL 即状态:?projectId=);预览环境按项目维度查询(契约要求 projectId)。
  - 卡片网格:每个预览一张卡。活跃卡高亮主色 + 实时链接;已回收卡灰显。
  - 摘要条:活跃 / 已回收计数。手动回收走 POST reclaim,乐观回写该卡。
  数据来自 GET /api/preview-envs?projectId=(只读列表)。
*/
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { NIcon } from 'naive-ui'
import { Rocket, GitPullRequest, ExternalLink, Recycle, Clock, GitBranch, Server } from '@vicons/tabler'
import {
  listPreviewEnvs,
  reclaimPreviewEnv,
  type PreviewEnv,
} from '../api/previewEnvs'
import { listProjects, type Project } from '../api/projects'
import { HttpError } from '../api/http'
import { useToast } from '../composables/useToast'
import { useConfirm } from '../composables/useConfirm'
import EmptyState from '../components/ui/EmptyState.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const toast = useToast()
const confirm = useConfirm()

// ─── URL as state:projectId 落 query ──────────────────────────────────────────
const projectId = computed<string>(() => {
  const p = route.query.projectId
  return typeof p === 'string' ? p : ''
})
function selectProject(id: string): void {
  void router.replace({ query: id ? { ...route.query, projectId: id } : {} })
}
function onProjectChange(e: Event): void {
  selectProject((e.target as HTMLSelectElement).value)
}

const projects = ref<Project[]>([])
const selectedProject = computed(() => projects.value.find((p) => p.id === projectId.value))

// ─── 列表加载 ──────────────────────────────────────────────────────────────────
type LoadState = 'idle' | 'loading' | 'loaded' | 'error' | 'noproject'
const loadState = ref<LoadState>('idle')
const loadError = ref('')
const envs = ref<PreviewEnv[]>([])
const refreshing = ref(false)

async function loadProjects(): Promise<void> {
  try {
    projects.value = await listProjects()
    // 没选项目时默认选第一个(契约要求按 projectId 查询)。
    if (!projectId.value && projects.value.length > 0) {
      selectProject(projects.value[0].id)
    }
  } catch {
    projects.value = []
  }
}

async function load(): Promise<void> {
  if (!projectId.value) {
    loadState.value = projects.value.length === 0 ? 'noproject' : 'idle'
    envs.value = []
    return
  }
  loadState.value = envs.value.length === 0 ? 'loading' : 'loaded'
  refreshing.value = true
  loadError.value = ''
  try {
    envs.value = await listPreviewEnvs(projectId.value)
    loadState.value = 'loaded'
  } catch (err) {
    loadState.value = envs.value.length === 0 ? 'error' : 'loaded'
    loadError.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? t('previewEnvs.board.errLoad', { status: err.status }))
        : t('previewEnvs.board.errNetwork')
  } finally {
    refreshing.value = false
  }
}

onMounted(async () => {
  await loadProjects()
  await load()
})

// 切项目 → 清空旧数据并重新拉。
watch(projectId, () => {
  envs.value = []
  void load()
})

// ─── 派生:活跃在前,创建时间倒序 ────────────────────────────────────────────────
const sorted = computed<PreviewEnv[]>(() => {
  return [...envs.value].sort((a, b) => {
    if (a.status !== b.status) return a.status === 'active' ? -1 : 1
    return b.createdAt.localeCompare(a.createdAt)
  })
})
const activeCount = computed(() => envs.value.filter((e) => e.status === 'active').length)
const reclaimedCount = computed(() => envs.value.filter((e) => e.status === 'reclaimed').length)

function fmtTime(s: string): string {
  if (!s) return '—'
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? s : d.toLocaleString()
}

// ─── 回收 ──────────────────────────────────────────────────────────────────────
const busy = ref<Set<string>>(new Set())
function isBusy(id: string): boolean {
  return busy.value.has(id)
}

async function reclaim(env: PreviewEnv): Promise<void> {
  if (isBusy(env.id) || env.status !== 'active') return
  const ok = await confirm.open({
    title: t('previewEnvs.board.reclaimConfirmTitle'),
    body: t('previewEnvs.board.reclaimConfirmBody', { sub: env.subdomain }),
    confirmLabel: t('previewEnvs.board.reclaim'),
    variant: 'danger',
  })
  if (!ok) return
  busy.value = new Set(busy.value).add(env.id)
  try {
    const res = await reclaimPreviewEnv(env.id)
    if (res.ok) {
      const nowIso = new Date().toISOString()
      envs.value = envs.value.map((e) =>
        e.id === env.id ? { ...e, status: 'reclaimed', reclaimedAt: nowIso } : e,
      )
      toast.success(t('previewEnvs.board.reclaimed'), { detail: env.subdomain })
    } else {
      toast.error(t('previewEnvs.board.reclaimFail'), { detail: env.subdomain })
    }
  } catch (err) {
    toast.error(t('previewEnvs.board.reclaimFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('previewEnvs.board.errReq', { status: err.status }))
          : t('previewEnvs.board.errNetwork'),
    })
  } finally {
    const next = new Set(busy.value)
    next.delete(env.id)
    busy.value = next
  }
}
</script>

<template>
  <div class="pvb">
    <header class="pvb__header">
      <div class="pvb__title-wrap">
        <span class="pvb__eyebrow">
          <NIcon :size="13"><Rocket /></NIcon>
          {{ t('previewEnvs.board.eyebrow') }}
        </span>
        <h1 class="pvb__title">{{ t('previewEnvs.board.title') }}</h1>
        <p class="pvb__sub">{{ t('previewEnvs.board.subtitle') }}</p>
      </div>
      <button class="pvb__refresh" :disabled="refreshing || !projectId" @click="load">
        <NIcon :size="14"><Recycle /></NIcon>
        {{ refreshing ? t('previewEnvs.board.refreshing') : t('common.refresh') }}
      </button>
    </header>

    <!-- 项目选择器 + 摘要 -->
    <div class="pvb__controls">
      <label class="pvb__field">
        <span class="pvb__field-lbl">{{ t('previewEnvs.board.projectLabel') }}</span>
        <select class="pvb__select" :value="projectId" @change="onProjectChange" :aria-label="t('previewEnvs.board.projectAria')">
          <option value="" disabled>{{ t('previewEnvs.board.projectPick') }}</option>
          <option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
      </label>
      <div v-if="loadState === 'loaded' && envs.length > 0" class="pvb__stats">
        <span class="pvb__stat pvb__stat--active">
          <span class="pvb__stat-dot" aria-hidden="true" />
          {{ t('previewEnvs.board.activeCount', { n: activeCount }) }}
        </span>
        <span class="pvb__stat pvb__stat--reclaimed">
          {{ t('previewEnvs.board.reclaimedCount', { n: reclaimedCount }) }}
        </span>
      </div>
    </div>

    <!-- 首屏骨架 -->
    <div v-if="loadState === 'loading'" class="pvb__grid" aria-busy="true">
      <SkeletonBlock v-for="n in 3" :key="n" :height="148" width="100%" />
    </div>

    <ErrorState
      v-else-if="loadState === 'error'"
      :title="t('previewEnvs.board.errTitle')"
      :description="loadError"
      @retry="load"
    />

    <EmptyState
      v-else-if="loadState === 'noproject'"
      :title="t('previewEnvs.board.noProjectTitle')"
      :description="t('previewEnvs.board.noProjectDesc')"
    />

    <EmptyState
      v-else-if="loadState === 'loaded' && envs.length === 0"
      :title="t('previewEnvs.board.emptyTitle')"
      :description="t('previewEnvs.board.emptyDesc', { project: selectedProject?.name ?? '' })"
    />

    <!-- 卡片网格 -->
    <div v-else-if="loadState === 'loaded'" class="pvb__grid">
      <article
        v-for="env in sorted"
        :key="env.id"
        class="pcard"
        :class="{ 'pcard--reclaimed': env.status === 'reclaimed', 'pcard--busy': isBusy(env.id) }"
      >
        <div class="pcard__top">
          <span class="pcard__pr">
            <NIcon :size="14"><GitPullRequest /></NIcon>
            <span class="pcard__pr-num">#{{ env.prNumber }}</span>
          </span>
          <span class="pcard__status" :class="`pcard__status--${env.status}`">
            {{ env.status === 'active' ? t('previewEnvs.board.statusActive') : t('previewEnvs.board.statusReclaimed') }}
          </span>
        </div>

        <a
          v-if="env.status === 'active'"
          class="pcard__url"
          :href="`https://${env.subdomain}`"
          target="_blank"
          rel="noopener noreferrer"
          :title="env.subdomain"
        >
          <span class="pcard__url-txt mono">{{ env.subdomain }}</span>
          <NIcon :size="14" class="pcard__url-ic"><ExternalLink /></NIcon>
        </a>
        <span v-else class="pcard__url pcard__url--dead mono" :title="env.subdomain">{{ env.subdomain }}</span>

        <dl class="pcard__meta">
          <div class="pcard__meta-row">
            <dt><NIcon :size="13"><GitBranch /></NIcon></dt>
            <dd class="mono" :title="env.branch">{{ env.branch }}</dd>
          </div>
          <div class="pcard__meta-row">
            <dt><NIcon :size="13"><Clock /></NIcon></dt>
            <dd>
              {{ env.status === 'active'
                ? t('previewEnvs.board.createdAt', { time: fmtTime(env.createdAt) })
                : t('previewEnvs.board.reclaimedAt', { time: fmtTime(env.reclaimedAt) }) }}
            </dd>
          </div>
          <div v-if="env.serverId" class="pcard__meta-row">
            <dt><NIcon :size="13"><Server /></NIcon></dt>
            <dd class="mono">{{ env.serverId }}</dd>
          </div>
        </dl>

        <div class="pcard__foot">
          <button
            v-if="env.status === 'active'"
            class="pcard__reclaim"
            :disabled="isBusy(env.id)"
            @click="reclaim(env)"
          >
            <NIcon :size="13"><Recycle /></NIcon>
            {{ isBusy(env.id) ? t('previewEnvs.board.reclaiming') : t('previewEnvs.board.reclaim') }}
          </button>
          <span v-else class="pcard__retired">{{ t('previewEnvs.board.retired') }}</span>
        </div>
      </article>
    </div>

    <p v-if="loadState === 'loaded' && envs.length > 0" class="pvb__foot">
      <NIcon :size="13" class="pvb__foot-ic"><Rocket /></NIcon>
      {{ t('previewEnvs.board.footNote') }}
    </p>
  </div>
</template>

<style scoped>
.pvb {
  padding: 28px clamp(16px, 4vw, 40px) 40px;
  max-width: 1180px;
  margin: 0 auto;
}

/* 头部 */
.pvb__header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 18px;
  flex-wrap: wrap;
  margin-bottom: 22px;
}
.pvb__eyebrow {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-micro);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.09em;
  color: var(--color-primary);
}
.pvb__title {
  margin: 6px 0 4px;
  font-size: var(--text-display);
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--color-text);
}
.pvb__sub {
  margin: 0;
  font-size: var(--text-label);
  color: var(--color-dim);
  max-width: 64ch;
  line-height: 1.55;
}
.pvb__refresh {
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
  transition: color var(--duration-fast, 150ms) ease, border-color var(--duration-fast, 150ms) ease;
}
.pvb__refresh:hover:not(:disabled) {
  color: var(--color-text);
  border-color: var(--color-text);
}
.pvb__refresh:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* 控件 */
.pvb__controls {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
  margin-bottom: 20px;
}
.pvb__field {
  display: flex;
  align-items: center;
  gap: 10px;
}
.pvb__field-lbl {
  font-size: var(--text-micro);
  font-weight: 600;
  color: var(--color-faint);
}
.pvb__select {
  height: 36px;
  padding: 0 12px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-text);
  font-size: var(--text-label);
  cursor: pointer;
  min-width: 200px;
}
.pvb__select:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.pvb__stats {
  display: inline-flex;
  align-items: center;
  gap: 14px;
}
.pvb__stat {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-micro);
  font-weight: 600;
}
.pvb__stat--active {
  color: var(--color-green);
}
.pvb__stat--reclaimed {
  color: var(--color-faint);
}
.pvb__stat-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-green);
  box-shadow: 0 0 0 3px var(--color-green-soft);
}

/* 卡片网格 */
.pvb__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 16px;
}

.pcard {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 13px;
  padding: 16px 17px 14px;
  border-radius: var(--rounded-card);
  border: 1px solid var(--color-border);
  background: var(--color-card);
  box-shadow: var(--shadow);
  overflow: hidden;
  transition: border-color var(--duration-fast, 150ms) ease, transform var(--duration-fast, 150ms) ease;
}
/* 活跃卡:顶部主色光条 + hover 微抬,营造「在线/可点」感 */
.pcard::before {
  content: '';
  position: absolute;
  inset: 0 0 auto 0;
  height: 3px;
  background: linear-gradient(90deg, var(--color-primary), var(--color-cyan));
}
.pcard:hover {
  transform: translateY(-2px);
  border-color: var(--color-border-strong);
}
.pcard--reclaimed {
  background: var(--color-card-2);
  box-shadow: none;
}
.pcard--reclaimed::before {
  background: var(--color-border-strong);
}
.pcard--reclaimed:hover {
  transform: none;
}
.pcard--busy {
  opacity: 0.6;
  pointer-events: none;
}

.pcard__top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.pcard__pr {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--color-text);
}
.pcard__pr-num {
  font-size: var(--text-body);
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.pcard__status {
  font-size: var(--text-micro);
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  padding: 3px 9px;
  border-radius: var(--rounded-full);
}
.pcard__status--active {
  color: var(--color-green);
  background: var(--color-green-soft);
}
.pcard__status--reclaimed {
  color: var(--color-faint);
  background: var(--color-inset);
}

.pcard__url {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 11px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border);
  background: var(--color-inset);
  text-decoration: none;
  transition: border-color var(--duration-fast, 150ms) ease, background var(--duration-fast, 150ms) ease;
}
a.pcard__url:hover {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}
.pcard__url-txt {
  flex: 1;
  min-width: 0;
  font-size: var(--text-micro);
  color: var(--color-primary);
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.pcard__url-ic {
  flex-shrink: 0;
  color: var(--color-primary);
}
.pcard__url--dead {
  color: var(--color-faint);
  text-decoration: line-through;
  cursor: default;
}
.pcard__url--dead .pcard__url-txt,
.pcard__url--dead {
  color: var(--color-faint);
}

.pcard__meta {
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 7px;
}
.pcard__meta-row {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: var(--text-micro);
  color: var(--color-dim);
}
.pcard__meta-row dt {
  display: inline-flex;
  color: var(--color-faint);
  flex-shrink: 0;
}
.pcard__meta-row dd {
  margin: 0;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.pcard__foot {
  display: flex;
  justify-content: flex-end;
  padding-top: 2px;
  border-top: 1px solid var(--color-border);
  margin-top: auto;
}
.pcard__reclaim {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-top: 11px;
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 6px 12px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
  transition: color var(--duration-fast, 150ms) ease, border-color var(--duration-fast, 150ms) ease, background var(--duration-fast, 150ms) ease;
}
.pcard__reclaim:hover:not(:disabled) {
  color: var(--color-red);
  border-color: var(--color-red-line);
  background: var(--color-red-soft);
}
.pcard__reclaim:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.pcard__retired {
  margin-top: 11px;
  font-size: var(--text-micro);
  color: var(--color-faint);
  font-style: italic;
}

.pvb__foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 18px 2px 0;
  font-size: var(--text-micro);
  color: var(--color-faint);
  line-height: 1.5;
}
.pvb__foot-ic {
  color: var(--color-primary);
  flex-shrink: 0;
}

@media (max-width: 560px) {
  .pvb__grid {
    grid-template-columns: 1fr;
  }
}
</style>
