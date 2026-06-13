<script setup lang="ts">
/**
 * SettingsSystem — 系统信息与一键检查更新。
 *
 * - 挂载时读 GET /version 显示当前构建版本与元数据(commit / 构建时间 / 平台 / Go 版本)。
 * - 「检查更新」按钮调 GET /api/version/check(查 GitHub 最新发布并比对):
 *     状态收敛到 hero 右上的状态药丸;仅当有新版时,下方展开强调升级卡(CTA + 可折叠更新说明)。
 * - 开发态(version=dev)永不误报更新,药丸显示「开发构建」。
 */

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { getVersion, checkUpdate, applyUpdate } from '../../api/version'
import type { VersionInfo, UpdateInfo } from '../../api/version'
import { getRetentionConfig, setRetentionConfig, type RetentionConfig } from '../../api/retention'
import { HttpError } from '../../api/http'
import AppButton from '../../components/ui/AppButton.vue'

const { t } = useI18n()

const version = ref<VersionInfo | null>(null)
const versionError = ref('')

type CheckState = 'idle' | 'checking' | 'done' | 'error'
const checkState = ref<CheckState>('idle')
const update = ref<UpdateInfo | null>(null)
const checkError = ref('')
const showNotes = ref(false)

// 一键自动更新状态机。
type UpdatePhase = 'idle' | 'applying' | 'restarting' | 'manual' | 'error'
const updatePhase = ref<UpdatePhase>('idle')
const updateMsg = ref('')
const dockerCommand = ref('')
const copied = ref(false)

const isDev = computed(() => {
  const v = version.value?.version ?? ''
  return v === '' || v === 'dev' || !/^v?\d/.test(v)
})

const hasUpdate = computed(() => checkState.value === 'done' && !!update.value?.updateAvailable)

/** hero 右上状态药丸:label + 色调(随检查状态变化)。 */
type Tone = 'neutral' | 'checking' | 'ok' | 'update' | 'dev' | 'err'
const pill = computed<{ label: string; tone: Tone }>(() => {
  if (checkState.value === 'checking') return { label: t('settingsSystem.pillChecking'), tone: 'checking' }
  if (checkState.value === 'error') return { label: t('settingsSystem.pillCheckFailed'), tone: 'err' }
  if (checkState.value === 'done') {
    if (hasUpdate.value) return { label: t('settingsSystem.pillUpdateAvailable'), tone: 'update' }
    if (isDev.value) return { label: t('settingsSystem.pillDevBuild'), tone: 'dev' }
    return { label: t('settingsSystem.pillUpToDate'), tone: 'ok' }
  }
  return { label: isDev.value ? t('settingsSystem.pillDevBuild') : t('settingsSystem.pillNotChecked'), tone: isDev.value ? 'dev' : 'neutral' }
})

const meta = computed(() => {
  const v = version.value
  if (!v) return []
  return [
    { label: t('settingsSystem.metaCommit'), value: v.commit, mono: true },
    { label: t('settingsSystem.metaBuildTime'), value: fmtDate(v.date) },
    { label: t('settingsSystem.metaPlatform'), value: v.platform, mono: true },
    { label: t('settingsSystem.metaGoVersion'), value: v.goVersion, mono: true },
  ].filter((m) => m.value && m.value !== 'none' && m.value !== 'unknown')
})

function fmtDate(s: string): string {
  if (!s || s === 'unknown') return s
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  return d.toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

async function loadVersion(): Promise<void> {
  try {
    version.value = await getVersion()
  } catch {
    versionError.value = t('settingsSystem.errReadVersion')
  }
}

async function runCheck(): Promise<void> {
  checkState.value = 'checking'
  checkError.value = ''
  update.value = null
  showNotes.value = false
  try {
    const info = await checkUpdate()
    if (info.checkError) {
      checkError.value = info.checkError
      checkState.value = 'error'
      return
    }
    update.value = info
    checkState.value = 'done'
  } catch (err) {
    checkError.value = err instanceof HttpError ? (err.apiError?.message ?? t('settingsSystem.errCheckStatus', { status: err.status })) : t('settingsSystem.errCheckRetry')
    checkState.value = 'error'
  }
}

// 一键自动更新:
//  - binary 模式:后端下载+替换二进制后自重启 → 前端进入"重启中"并轮询 /version,
//    版本变化即整页 reload 到新版。
//  - docker 模式:后端返回升级命令,前端展示 + 复制(容器不能自换镜像)。
async function runUpdate(): Promise<void> {
  const from = update.value?.current ?? version.value?.version ?? ''
  updatePhase.value = 'applying'
  updateMsg.value = ''
  dockerCommand.value = ''
  try {
    const res = await applyUpdate()
    if (res.status === 'restarting') {
      updatePhase.value = 'restarting'
      updateMsg.value = res.message || t('settingsSystem.restartingToNew')
      pollUntilRestarted(from)
    } else if (res.status === 'manual' && res.mode === 'docker') {
      updatePhase.value = 'manual'
      dockerCommand.value = res.command ?? ''
      updateMsg.value = res.message || ''
    } else if (res.status === 'manual') {
      updatePhase.value = 'manual'
      updateMsg.value = res.message || t('settingsSystem.manualUpgrade')
    } else if (res.status === 'uptodate') {
      updatePhase.value = 'idle'
      void runCheck()
    } else {
      updatePhase.value = 'error'
      updateMsg.value = res.message || t('settingsSystem.updateFailed')
    }
  } catch (err) {
    updatePhase.value = 'error'
    updateMsg.value =
      err instanceof HttpError ? (err.apiError?.message ?? t('settingsSystem.errUpdateStatus', { status: err.status })) : t('settingsSystem.errUpdateRetry')
  }
}

// 轮询 /version,直到版本号与旧版不同(新进程已起),然后整页刷新加载新版前端。
function pollUntilRestarted(from: string): void {
  let tries = 0
  const timer = window.setInterval(async () => {
    tries++
    try {
      const v = await getVersion()
      if (v.version && v.version !== from) {
        window.clearInterval(timer)
        window.location.reload()
        return
      }
    } catch {
      // 重启窗口内连接会短暂失败,忽略重试。
    }
    if (tries > 40) {
      // ~60s 仍未起来:停止轮询,提示手动刷新(更新已就位,可能需手动重启)。
      window.clearInterval(timer)
      updatePhase.value = 'error'
      updateMsg.value = t('settingsSystem.restartTimeout')
    }
  }, 1500)
}

async function copyCommand(): Promise<void> {
  try {
    await navigator.clipboard.writeText(dockerCommand.value)
    copied.value = true
    window.setTimeout(() => (copied.value = false), 1800)
  } catch {
    // 剪贴板不可用(非 https / 权限):静默,用户可手动选中复制。
  }
}

// ─── 运行数据保留策略 ─────────────────────────────────────────────────────────
const rt = ref<RetentionConfig>({ enabled: false, keepPerProject: 0, maxAgeDays: 0 })
const rtLoading = ref(true)
const rtSaving = ref(false)
const rtSaved = ref(false)
const rtError = ref('')

async function loadRetention(): Promise<void> {
  rtLoading.value = true
  try {
    rt.value = await getRetentionConfig()
  } catch {
    // 读失败保持默认(关),不阻断页面
  } finally {
    rtLoading.value = false
  }
}

async function saveRetention(): Promise<void> {
  rtSaving.value = true
  rtSaved.value = false
  rtError.value = ''
  try {
    rt.value = await setRetentionConfig({
      enabled: rt.value.enabled,
      keepPerProject: Math.max(0, Math.floor(Number(rt.value.keepPerProject) || 0)),
      maxAgeDays: Math.max(0, Math.floor(Number(rt.value.maxAgeDays) || 0)),
    })
    rtSaved.value = true
    setTimeout(() => { rtSaved.value = false }, 2000)
  } catch {
    rtError.value = t('settingsSystem.retentionSaveFailed')
  } finally {
    rtSaving.value = false
  }
}

onMounted(() => {
  void loadVersion()
  void loadRetention()
})
</script>

<template>
  <section class="sys" aria-labelledby="sys-heading">
    <header class="sys-head">
      <h2 id="sys-heading" class="sys-title">{{ t('settingsSystem.title') }}</h2>
      <p class="sys-sub">{{ t('settingsSystem.subtitle') }}</p>
    </header>

    <!-- 版本面板 -->
    <article class="sys-panel">
      <span class="sys-accent" aria-hidden="true" />

      <!-- hero:品牌 + 状态药丸 -->
      <div class="sys-hero">
        <div class="sys-brand">
          <span class="sys-glyph mono" aria-hidden="true">p&gt;</span>
          <span class="sys-brand-text">
            <span class="sys-brand-name">Pipewright</span>
            <span class="sys-brand-tag">{{ t('settingsSystem.brandTag') }}</span>
          </span>
        </div>
        <span class="sys-pill" :class="`sys-pill--${pill.tone}`">
          <span class="sys-pill-dot" aria-hidden="true" />
          {{ pill.label }}
        </span>
      </div>

      <!-- 版本号 + 操作 -->
      <div class="sys-version-row">
        <div class="sys-version-block">
          <span class="sys-version-cap">{{ t('settingsSystem.currentVersion') }}</span>
          <strong class="sys-version-num" :class="{ 'is-dev': isDev }">{{ version?.version ?? '…' }}</strong>
        </div>
        <AppButton variant="primary" :loading="checkState === 'checking'" @click="runCheck">
          <svg v-if="checkState !== 'checking'" class="sys-btn-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M1 4v6h6M23 20v-6h-6" /><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
          </svg>
          {{ checkState === 'checking' ? t('settingsSystem.pillChecking') : t('settingsSystem.checkUpdate') }}
        </AppButton>
      </div>

      <p v-if="versionError" class="sys-inline-err" role="alert">{{ versionError }}</p>
      <p v-else-if="checkState === 'error'" class="sys-inline-err" role="alert">{{ t('settingsSystem.checkFailedPrefix', { msg: checkError }) }}</p>

      <!-- 构建元数据 -->
      <dl v-if="meta.length" class="sys-meta">
        <div v-for="m in meta" :key="m.label" class="sys-meta-item">
          <dt>{{ m.label }}</dt>
          <dd :class="{ mono: m.mono }">{{ m.value }}</dd>
        </div>
      </dl>
    </article>

    <!-- 仅当有新版时:强调升级卡 -->
    <transition name="sys-rise">
      <article v-if="hasUpdate" class="sys-upgrade">
        <div class="sys-upgrade-head">
          <span class="sys-upgrade-icon" aria-hidden="true">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7" /></svg>
          </span>
          <div class="sys-upgrade-meta">
            <h3 class="sys-upgrade-title">{{ t('settingsSystem.upgradeTitle') }}</h3>
            <div class="sys-vjump">
              <span class="sys-vtag sys-vtag--cur">{{ update?.current }}</span>
              <span class="sys-varrow" aria-hidden="true">→</span>
              <span class="sys-vtag sys-vtag--new">{{ update?.latest }}</span>
              <span v-if="update?.publishedAt" class="sys-pubdate">{{ t('settingsSystem.publishedAt', { date: fmtDate(update.publishedAt) }) }}</span>
            </div>
          </div>
          <div class="sys-upgrade-actions">
            <AppButton
              variant="primary"
              :loading="updatePhase === 'applying' || updatePhase === 'restarting'"
              :disabled="updatePhase === 'restarting'"
              @click="runUpdate"
            >
              {{ updatePhase === 'applying' ? t('settingsSystem.preparing') : updatePhase === 'restarting' ? t('settingsSystem.restarting') : t('settingsSystem.updateNow') }}
            </AppButton>
            <a v-if="update?.releaseUrl" class="sys-cta-ghost" :href="update.releaseUrl" target="_blank" rel="noopener noreferrer">
              {{ t('settingsSystem.viewRelease') }}
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M7 17L17 7M7 7h10v10" /></svg>
            </a>
          </div>
        </div>

        <!-- 自动更新进行态 -->
        <transition name="sys-rise">
          <div v-if="updatePhase === 'restarting'" class="sys-update-state sys-update-state--busy" role="status">
            <span class="sys-spinner-dark" aria-hidden="true" />
            <span>{{ t('settingsSystem.reconnecting', { msg: updateMsg }) }}</span>
          </div>
          <div v-else-if="updatePhase === 'manual'" class="sys-update-state sys-update-state--manual">
            <p class="sys-update-state-msg">{{ updateMsg }}</p>
            <div v-if="dockerCommand" class="sys-cmd">
              <code>{{ dockerCommand }}</code>
              <button type="button" class="sys-cmd-copy" @click="copyCommand">{{ copied ? t('settingsSystem.copied') : t('settingsSystem.copy') }}</button>
            </div>
          </div>
          <div v-else-if="updatePhase === 'error'" class="sys-update-state sys-update-state--err" role="alert">
            {{ t('settingsSystem.updateFailedPrefix', { msg: updateMsg }) }}
          </div>
        </transition>

        <div v-if="update?.notes" class="sys-notes-wrap">
          <button class="sys-notes-toggle" :aria-expanded="showNotes" @click="showNotes = !showNotes">
            <svg class="sys-chevron" :class="{ open: showNotes }" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 18l6-6-6-6" /></svg>
            {{ showNotes ? t('settingsSystem.hideNotes') : t('settingsSystem.showNotes') }}
          </button>
          <transition name="sys-rise">
            <pre v-if="showNotes" class="sys-notes">{{ update.notes }}</pre>
          </transition>
        </div>
      </article>
    </transition>

    <!-- 运行数据保留 / 清理 -->
    <article class="sys-panel">
      <span class="sys-accent" aria-hidden="true" />
      <div class="rt-head">
        <div class="rt-head-text">
          <h3 class="rt-title">{{ t('settingsSystem.retentionTitle') }}</h3>
          <p class="rt-sub">{{ t('settingsSystem.retentionSub') }}</p>
        </div>
        <button
          type="button"
          class="rt-switch"
          :class="{ 'rt-switch--on': rt.enabled }"
          role="switch"
          :aria-checked="rt.enabled"
          :aria-label="t('settingsSystem.retentionTitle')"
          :disabled="rtLoading"
          @click="rt.enabled = !rt.enabled"
        >
          <span class="rt-switch-knob" aria-hidden="true" />
        </button>
      </div>

      <div class="rt-fields" :class="{ 'rt-fields--off': !rt.enabled }">
        <label class="rt-field">
          <span class="rt-label">{{ t('settingsSystem.retentionKeep') }}</span>
          <input class="rt-input mono" type="number" min="0" step="1" v-model.number="rt.keepPerProject" :disabled="!rt.enabled || rtLoading" />
          <span class="rt-hint">{{ t('settingsSystem.retentionKeepHint') }}</span>
        </label>
        <label class="rt-field">
          <span class="rt-label">{{ t('settingsSystem.retentionAge') }}</span>
          <input class="rt-input mono" type="number" min="0" step="1" v-model.number="rt.maxAgeDays" :disabled="!rt.enabled || rtLoading" />
          <span class="rt-hint">{{ t('settingsSystem.retentionAgeHint') }}</span>
        </label>
      </div>

      <p class="rt-warn">{{ t('settingsSystem.retentionWarn') }}</p>

      <div class="rt-actions">
        <span v-if="rtSaved" class="rt-saved" role="status">{{ t('settingsSystem.retentionSaved') }}</span>
        <span v-if="rtError" class="rt-err" role="alert">{{ rtError }}</span>
        <AppButton variant="primary" :loading="rtSaving" :disabled="rtLoading" @click="saveRetention">
          {{ t('settingsSystem.retentionSave') }}
        </AppButton>
      </div>
    </article>
  </section>
</template>

<style scoped>
.sys {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  max-width: 680px;
}

.sys-head {
  margin-bottom: var(--space-1);
}
.sys-title {
  font-size: var(--text-h2);
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--color-text);
}
.sys-sub {
  font-size: var(--text-body);
  color: var(--color-faint);
  margin-top: 2px;
}

/* ── 版本面板 ───────────────────────────── */
.sys-panel {
  position: relative;
  overflow: hidden;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--space-5);
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.04), 0 8px 24px -16px rgba(0, 0, 0, 0.18);
}
/* 顶部品牌强调条 */
.sys-accent {
  position: absolute;
  inset: 0 0 auto 0;
  height: 3px;
  background: linear-gradient(90deg, var(--color-primary), var(--color-primary-press) 60%, transparent);
}

/* hero */
.sys-hero {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}
.sys-brand {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.sys-glyph {
  width: 40px;
  height: 40px;
  border-radius: var(--rounded, 10px);
  display: grid;
  place-items: center;
  background: linear-gradient(140deg, var(--color-primary), var(--color-primary-press));
  color: #fff;
  font-family: var(--font-mono);
  font-weight: 700;
  font-size: 0.92rem;
  box-shadow: 0 6px 18px -4px var(--color-primary-soft);
  flex: none;
}
.sys-brand-text {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}
.sys-brand-name {
  font-size: var(--text-body);
  font-weight: 700;
  color: var(--color-text);
  letter-spacing: -0.01em;
}
.sys-brand-tag {
  font-size: var(--text-micro);
  color: var(--color-faint);
}

/* 状态药丸 */
.sys-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex: none;
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 4px 10px 4px 8px;
  border-radius: 999px;
  border: 1px solid transparent;
  white-space: nowrap;
}
.sys-pill-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
  flex: none;
}
.sys-pill--neutral {
  color: var(--color-dim);
  background: var(--color-inset);
  border-color: var(--color-border);
}
.sys-pill--checking {
  color: var(--color-dim);
  background: var(--color-inset);
  border-color: var(--color-border);
}
.sys-pill--checking .sys-pill-dot {
  animation: sys-blink 1s ease-in-out infinite;
}
.sys-pill--ok {
  color: var(--color-success);
  background: var(--color-green-soft);
  border-color: var(--color-green-line);
}
.sys-pill--update {
  color: var(--color-primary);
  background: var(--color-primary-soft);
  border-color: var(--color-primary);
}
.sys-pill--update .sys-pill-dot {
  animation: sys-pulse 1.5s ease-out infinite;
}
.sys-pill--dev {
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border-color: var(--color-amber-line);
}
.sys-pill--err {
  color: var(--color-danger);
  background: var(--color-danger-soft);
  border-color: var(--color-red-line, var(--color-danger));
}

/* 版本号 + 操作 */
.sys-version-row {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-3);
  flex-wrap: wrap;
}
.sys-version-block {
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.sys-version-cap {
  font-size: var(--text-micro);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.1em;
  color: var(--color-faint);
}
.sys-version-num {
  font-size: var(--text-kpi);
  font-weight: 800;
  line-height: 1;
  letter-spacing: -0.03em;
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
  font-family: var(--font-mono);
}
.sys-version-num.is-dev {
  color: var(--color-dim);
}
.sys-btn-icon {
  margin-right: 1px;
}

/* 元数据 */
.sys-meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3) var(--space-5);
  margin: 0;
  padding-top: var(--space-4);
  border-top: 1px solid var(--color-border);
}
.sys-meta-item {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}
.sys-meta-item dt {
  font-size: var(--text-micro);
  color: var(--color-faint);
  text-transform: uppercase;
  letter-spacing: 0.07em;
}
.sys-meta-item dd {
  margin: 0;
  font-size: var(--text-caption);
  color: var(--color-text-soft);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sys-meta-item dd.mono {
  font-family: var(--font-mono);
  font-size: var(--text-label);
}

.sys-inline-err {
  font-size: var(--text-caption);
  color: var(--color-danger);
  margin: -4px 0 0;
}

/* ── 升级强调卡 ─────────────────────────── */
.sys-upgrade {
  position: relative;
  border: 1px solid var(--color-primary);
  border-radius: var(--radius-lg);
  padding: var(--space-4) var(--space-5);
  background:
    linear-gradient(180deg, var(--color-primary-soft), transparent 70%),
    var(--color-card);
  box-shadow: 0 10px 30px -18px var(--color-primary);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
.sys-upgrade-head {
  display: flex;
  align-items: center;
  gap: 14px;
}
.sys-upgrade-icon {
  width: 38px;
  height: 38px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--color-primary);
  color: #fff;
  flex: none;
  box-shadow: 0 6px 16px -4px var(--color-primary-soft);
  animation: sys-bob 2.4s ease-in-out infinite;
}
.sys-upgrade-meta {
  flex: 1;
  min-width: 0;
}
.sys-upgrade-title {
  font-size: var(--text-body);
  font-weight: 700;
  color: var(--color-text);
  letter-spacing: -0.01em;
}
.sys-vjump {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-top: 5px;
  flex-wrap: wrap;
}
.sys-vtag {
  font-family: var(--font-mono);
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 6px;
}
.sys-vtag--cur {
  color: var(--color-dim);
  background: var(--color-inset);
  border: 1px solid var(--color-border);
}
.sys-vtag--new {
  color: var(--color-primary);
  background: var(--color-primary-soft);
  border: 1px solid var(--color-primary);
}
.sys-varrow {
  color: var(--color-faint);
  font-weight: 700;
}
.sys-pubdate {
  font-size: var(--text-micro);
  color: var(--color-faint);
}

/* 升级操作组:立即更新(主)+ 查看发布(次,ghost 链接) */
.sys-upgrade-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: none;
}
.sys-cta-ghost {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-primary);
  text-decoration: none;
  white-space: nowrap;
  transition: color var(--duration-fast);
}
.sys-cta-ghost:hover {
  color: var(--color-primary-press);
  text-decoration: underline;
}

/* 自动更新进行态 */
.sys-update-state {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-md);
  font-size: var(--text-caption);
  border: 1px solid transparent;
}
.sys-update-state--busy {
  background: var(--color-primary-soft);
  border-color: var(--color-primary);
  color: var(--color-text);
}
.sys-update-state--manual {
  flex-direction: column;
  align-items: stretch;
  gap: var(--space-2);
  background: var(--color-amber-soft);
  border-color: var(--color-amber-line);
  color: var(--color-text);
}
.sys-update-state-msg {
  margin: 0;
  color: var(--color-text-soft);
}
.sys-update-state--err {
  background: var(--color-danger-soft);
  border-color: var(--color-red-line, var(--color-danger));
  color: var(--color-danger);
}
.sys-spinner-dark {
  width: 14px;
  height: 14px;
  border: 2px solid var(--color-primary-soft);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: sys-spin 0.7s linear infinite;
  flex: none;
}
@keyframes sys-spin {
  to {
    transform: rotate(360deg);
  }
}

/* docker 升级命令 + 复制 */
.sys-cmd {
  display: flex;
  align-items: center;
  gap: 8px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  padding: 6px 6px 6px 12px;
}
.sys-cmd code {
  flex: 1;
  font-family: var(--font-mono);
  font-size: var(--text-micro);
  color: var(--color-text);
  overflow-x: auto;
  white-space: nowrap;
}
.sys-cmd-copy {
  flex: none;
  padding: 4px 10px;
  font-size: var(--text-micro);
  font-weight: 600;
  color: var(--color-primary);
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition:
    background var(--duration-fast),
    border-color var(--duration-fast);
}
.sys-cmd-copy:hover {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}

/* 更新说明折叠 */
.sys-notes-wrap {
  border-top: 1px dashed var(--color-border);
  padding-top: var(--space-3);
}
.sys-notes-toggle {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-primary);
  transition: color var(--duration-fast);
}
.sys-notes-toggle:hover {
  color: var(--color-primary-press);
}
.sys-chevron {
  transition: transform var(--duration-fast) var(--ease-out-expo, ease);
}
.sys-chevron.open {
  transform: rotate(90deg);
}
.sys-notes {
  margin: var(--space-3) 0 0;
  padding: var(--space-3) var(--space-4);
  max-height: 220px;
  overflow: auto;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  font-size: var(--text-micro);
  line-height: 1.6;
  color: var(--color-text-soft);
  white-space: pre-wrap;
  word-break: break-word;
}

/* ── 动效 ───────────────────────────────── */
@keyframes sys-blink {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}
@keyframes sys-pulse {
  0% { box-shadow: 0 0 0 0 var(--color-primary-soft); }
  100% { box-shadow: 0 0 0 6px transparent; }
}
@keyframes sys-bob {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-3px); }
}
.sys-rise-enter-active,
.sys-rise-leave-active {
  transition:
    opacity var(--duration-normal),
    transform var(--duration-normal) var(--ease-out-expo, ease);
}
.sys-rise-enter-from,
.sys-rise-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}

@media (prefers-reduced-motion: reduce) {
  .sys-upgrade-icon,
  .sys-pill-dot {
    animation: none;
  }
}

/* ─── 运行数据保留 ───────────────────────────────────────────────────────────── */
.rt-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}
.rt-title {
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-text);
  margin: 0 0 4px;
}
.rt-sub {
  font-size: 0.82rem;
  color: var(--color-faint);
  margin: 0;
  max-width: 52ch;
}
.rt-switch {
  position: relative;
  flex: none;
  width: 40px;
  height: 22px;
  border-radius: 999px;
  border: none;
  cursor: pointer;
  background: var(--color-border);
  transition: background var(--duration-fast);
}
.rt-switch--on { background: var(--color-primary); }
.rt-switch:disabled { opacity: 0.55; cursor: default; }
.rt-switch:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.rt-switch-knob {
  position: absolute;
  top: 3px;
  left: 3px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #fff;
  transition: transform var(--duration-fast);
}
.rt-switch--on .rt-switch-knob { transform: translateX(18px); }

.rt-fields {
  display: flex;
  gap: 24px;
  flex-wrap: wrap;
  margin-top: 18px;
  transition: opacity var(--duration-fast);
}
.rt-fields--off { opacity: 0.5; }
.rt-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 180px;
}
.rt-label {
  font-size: 0.82rem;
  font-weight: 500;
  color: var(--color-dim);
}
.rt-input {
  width: 120px;
  padding: 7px 10px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
  color: var(--color-text);
  font-size: 0.9rem;
}
.rt-input:focus { outline: 2px solid var(--color-primary); outline-offset: -1px; }
.rt-input:disabled { opacity: 0.6; }
.rt-hint {
  font-size: 0.74rem;
  color: var(--color-faint);
}
.rt-warn {
  margin: 16px 0 0;
  font-size: 0.78rem;
  color: var(--color-faint);
  border-left: 2px solid var(--color-border);
  padding-left: 10px;
}
.rt-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 18px;
}
.rt-saved { font-size: 0.82rem; color: var(--color-ok, #18a058); }
.rt-err { font-size: 0.82rem; color: var(--color-danger, #e5484d); }
</style>
