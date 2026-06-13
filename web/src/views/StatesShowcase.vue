<script setup lang="ts">
/**
 * StatesShowcase — living styleguide page.
 * Route: /states  (public: true — no auth needed for dev browsing)
 * Demos every component in every state. Theme toggle built in.
 *
 * Sections:
 *  01 — StatusBadge (6 states)
 *  02 — AppButton (all variants + states)
 *  03 — Toast (4 types + action demo)
 *  04 — AppBanner (4 variants)
 *  05 — SkeletonBlock
 *  06 — EmptyState
 *  07 — ErrorState (error + ai variants)
 *  08 — ConfirmDialog (normal + type-to-confirm)
 *  09 — FormField (all input states)
 *  10 — AppTooltip
 *  11 — ProgressBar (determinate + indeterminate)
 */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useThemeStore } from '../stores/theme'
import { useToast } from '../composables/useToast'
import { useConfirm } from '../composables/useConfirm'
import StatusBadge   from '../components/ui/StatusBadge.vue'
import AppButton     from '../components/ui/AppButton.vue'
import AppBanner     from '../components/ui/AppBanner.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'
import EmptyState    from '../components/ui/EmptyState.vue'
import ErrorState    from '../components/ui/ErrorState.vue'
import FormField     from '../components/ui/FormField.vue'
import AppTooltip   from '../components/ui/AppTooltip.vue'
import ProgressBar   from '../components/ui/ProgressBar.vue'

const { t } = useI18n()
const themeStore = useThemeStore()
const toast = useToast()
const confirm = useConfirm()

// ——— Section 02: Button loading state demos ———
const loadingPrimary = ref(false)
function demoLoadPrimary() {
  loadingPrimary.value = true
  setTimeout(() => { loadingPrimary.value = false }, 2000)
}

// ——— Section 03: Toast demos ———
function fireSuccessToast() {
  toast.success(t('statesShowcase.toastSuccessTitle'), { detail: t('statesShowcase.toastSuccessDetail') })
}
function fireErrorToast() {
  toast.error(t('statesShowcase.toastErrorTitle'), {
    detail: t('statesShowcase.toastErrorDetail'),
    action: { label: t('statesShowcase.toastErrorAction'), onClick: () => console.log('AI diag') },
  })
}
function fireWarnToast() {
  toast.warn(t('statesShowcase.toastWarnTitle'), { detail: t('statesShowcase.toastWarnDetail') })
}
function fireInfoToast() {
  toast.info(t('statesShowcase.toastInfoTitle'), {
    detail: t('statesShowcase.toastInfoDetail'),
    action: { label: t('statesShowcase.toastInfoAction'), onClick: () => console.log('view') },
  })
}

// ——— Section 08: Confirm demos ———
async function demoConfirmSimple() {
  const ok = await confirm.open({
    title: t('statesShowcase.confirmRollbackTitle'),
    body: t('statesShowcase.confirmRollbackBody'),
    confirmLabel: t('statesShowcase.confirmRollbackLabel'),
    variant: 'danger',
  })
  toast[ok ? 'success' : 'info'](ok ? t('statesShowcase.confirmRollbackOk') : t('statesShowcase.confirmCancelled'))
}

async function demoConfirmTypeToConfirm() {
  const ok = await confirm.open({
    title: t('statesShowcase.confirmResetTitle'),
    body: t('statesShowcase.confirmResetBody'),
    confirmText: 'acme',
    confirmLabel: t('statesShowcase.confirmResetLabel'),
    variant: 'danger',
  })
  toast[ok ? 'success' : 'info'](ok ? t('statesShowcase.confirmResetOk') : t('statesShowcase.confirmCancelled'))
}

// ——— Section 09: Form fields ———
const fieldDefault = ref('')
const fieldError   = ref('non-valid-url')
const fieldHint    = ref('')
const fieldDisabled = ref(t('statesShowcase.formDisabledValue'))

// ——— Section 11: Progress ———
const progressVal = ref(55)
</script>

<template>
  <div class="showcase-page">
    <!-- Page header -->
    <header class="showcase-head">
      <div class="showcase-head__kk">
        <h1 class="showcase-head__title">{{ t('statesShowcase.pageTitle') }}</h1>
        <span class="showcase-head__tag">{{ t('statesShowcase.pageTag') }}</span>
      </div>
      <p class="showcase-head__desc">
        {{ t('statesShowcase.pageDescLead') }}
        <strong>{{ t('statesShowcase.pageDescTokens') }}</strong>{{ t('statesShowcase.pageDescTokensNote') }}
        {{ t('statesShowcase.pageDescMotionPre') }} <code class="mono">transform/opacity</code> {{ t('statesShowcase.pageDescMotionMid') }} <code class="mono">prefers-reduced-motion</code>{{ t('statesShowcase.pageDescMotionEnd') }}
      </p>
    </header>

    <div class="showcase-wrap">

      <!-- ============================================================
           01 StatusBadge
      ============================================================ -->
      <section class="sec">
        <div class="sec__head">
          <span class="sec__no">01</span>
          <h2 class="sec__title">{{ t('statesShowcase.sec01Title') }}</h2>
          <span class="sec__spec">{{ t('statesShowcase.sec01Spec') }}</span>
        </div>
        <div class="sec__body">
          <div class="demos">
            <div class="dcell">
              <span class="vlab">success</span>
              <StatusBadge status="success" />
            </div>
            <div class="dcell">
              <span class="vlab">failed</span>
              <StatusBadge status="failed" />
            </div>
            <div class="dcell">
              <span class="vlab">running</span>
              <StatusBadge status="running" />
            </div>
            <div class="dcell">
              <span class="vlab">partial</span>
              <StatusBadge status="partial" />
            </div>
            <div class="dcell">
              <span class="vlab">rolledback</span>
              <StatusBadge status="rolledback" />
            </div>
            <div class="dcell">
              <span class="vlab">queued</span>
              <StatusBadge status="queued" />
            </div>
          </div>
        </div>
      </section>

      <!-- ============================================================
           02 AppButton
      ============================================================ -->
      <section class="sec">
        <div class="sec__head">
          <span class="sec__no">02</span>
          <h2 class="sec__title">{{ t('statesShowcase.sec02Title') }}</h2>
          <span class="sec__spec">{{ t('statesShowcase.sec02Spec') }}</span>
        </div>
        <div class="sec__body">
          <p class="subt">{{ t('statesShowcase.sec02SubHierarchy') }}</p>
          <div class="demos">
            <div class="dcell">
              <span class="vlab">primary</span>
              <AppButton variant="primary">{{ t('statesShowcase.btnRedeploy') }}</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">default</span>
              <AppButton variant="default">{{ t('statesShowcase.btnViewLogs') }}</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">ghost</span>
              <AppButton variant="ghost">{{ t('statesShowcase.btnCancel') }}</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">danger</span>
              <AppButton variant="danger">{{ t('statesShowcase.btnRollback126') }}</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">ai</span>
              <AppButton variant="ai">{{ t('statesShowcase.btnAiDiagnosis') }}</AppButton>
            </div>
          </div>

          <p class="subt" style="margin-top:18px">{{ t('statesShowcase.sec02SubPrimaryStates') }}</p>
          <div class="demos">
            <div class="dcell">
              <span class="vlab">default</span>
              <AppButton variant="primary">{{ t('statesShowcase.btnDefault') }}</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">loading</span>
              <AppButton variant="primary" :loading="loadingPrimary" @click="demoLoadPrimary">
                {{ loadingPrimary ? t('statesShowcase.btnSubmitting') : t('statesShowcase.btnClickToLoad') }}
              </AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">disabled</span>
              <AppButton variant="primary" disabled>{{ t('statesShowcase.btnDisabled') }}</AppButton>
            </div>
          </div>
        </div>
      </section>

      <!-- ============================================================
           03 Toast
      ============================================================ -->
      <section class="sec">
        <div class="sec__head">
          <span class="sec__no">03</span>
          <h2 class="sec__title">{{ t('statesShowcase.sec03Title') }}</h2>
          <span class="sec__spec">{{ t('statesShowcase.sec03Spec') }}</span>
        </div>
        <div class="sec__body">
          <div class="demos">
            <div class="dcell">
              <span class="vlab">{{ t('statesShowcase.toastLabelSuccess') }}</span>
              <AppButton variant="default" @click="fireSuccessToast">{{ t('statesShowcase.toastTriggerSuccess') }}</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">{{ t('statesShowcase.toastLabelError') }}</span>
              <AppButton variant="danger" @click="fireErrorToast">{{ t('statesShowcase.toastTriggerError') }}</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">{{ t('statesShowcase.toastLabelWarn') }}</span>
              <AppButton variant="default" @click="fireWarnToast">{{ t('statesShowcase.toastTriggerWarn') }}</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">{{ t('statesShowcase.toastLabelInfo') }}</span>
              <AppButton variant="ai" @click="fireInfoToast">{{ t('statesShowcase.toastTriggerInfo') }}</AppButton>
            </div>
          </div>
          <p class="demo-hint">{{ t('statesShowcase.toastHint') }}</p>
        </div>
      </section>

      <!-- ============================================================
           04 AppBanner
      ============================================================ -->
      <section class="sec">
        <div class="sec__head">
          <span class="sec__no">04</span>
          <h2 class="sec__title">{{ t('statesShowcase.sec04Title') }}</h2>
          <span class="sec__spec">{{ t('statesShowcase.sec04Spec') }}</span>
        </div>
        <div class="sec__body" style="display:flex;flex-direction:column;gap:10px;">
          <AppBanner variant="info">
            {{ t('statesShowcase.bannerInfoBody') }}
            <template #action>{{ t('statesShowcase.bannerInfoAction') }}</template>
          </AppBanner>

          <AppBanner variant="warn" :title="t('statesShowcase.bannerWarnTitle')">
            {{ t('statesShowcase.bannerWarnBody') }}
          </AppBanner>

          <AppBanner variant="error" :title="t('statesShowcase.bannerErrorTitle')">
            {{ t('statesShowcase.bannerErrorBody') }}
            <template #action>{{ t('statesShowcase.bannerErrorAction') }}</template>
          </AppBanner>

          <AppBanner variant="success">
            {{ t('statesShowcase.bannerSuccessBody') }}
          </AppBanner>

          <AppBanner variant="ai" :title="t('statesShowcase.bannerAiTitle')">
            {{ t('statesShowcase.bannerAiBody') }}
          </AppBanner>
        </div>
      </section>

      <!-- ============================================================
           05 + 06: Skeleton + Empty in 2-col
      ============================================================ -->
      <div class="grid2">
        <!-- 05 Skeleton -->
        <section class="sec">
          <div class="sec__head">
            <span class="sec__no">05</span>
            <h2 class="sec__title">{{ t('statesShowcase.sec05Title') }}</h2>
            <span class="sec__spec">{{ t('statesShowcase.sec05Spec') }}</span>
          </div>
          <div class="sec__body">
            <div class="sk-demo-card">
              <!-- Avatar row -->
              <div class="sk-row">
                <SkeletonBlock :width="40" :height="40" circle />
                <div class="sk-col">
                  <SkeletonBlock :height="11" width="60%" />
                  <SkeletonBlock :height="11" width="40%" />
                </div>
              </div>
              <!-- Lines -->
              <SkeletonBlock :height="11" width="100%" style="margin-bottom:9px" />
              <SkeletonBlock :height="11" width="80%" style="margin-bottom:9px" />
              <SkeletonBlock :height="11" width="60%" />
            </div>
          </div>
        </section>

        <!-- 06 Empty -->
        <section class="sec">
          <div class="sec__head">
            <span class="sec__no">06</span>
            <h2 class="sec__title">{{ t('statesShowcase.sec06Title') }}</h2>
            <span class="sec__spec">{{ t('statesShowcase.sec06Spec') }}</span>
          </div>
          <div class="sec__body">
            <EmptyState
              :title="t('statesShowcase.emptyTitle')"
              :description="t('statesShowcase.emptyDescription')"
            >
              <template #cta>
                <AppButton variant="primary">{{ t('statesShowcase.emptyCta') }}</AppButton>
              </template>
            </EmptyState>
          </div>
        </section>
      </div>

      <!-- ============================================================
           07 ErrorState
      ============================================================ -->
      <section class="sec">
        <div class="sec__head">
          <span class="sec__no">07</span>
          <h2 class="sec__title">{{ t('statesShowcase.sec07Title') }}</h2>
          <span class="sec__spec">{{ t('statesShowcase.sec07Spec') }}</span>
        </div>
        <div class="sec__body">
          <div class="grid2">
            <div>
              <p class="subt">{{ t('statesShowcase.errPageSub') }}</p>
              <ErrorState
                :title="t('statesShowcase.errPageTitle')"
                :description="t('statesShowcase.errPageDescription')"
                @retry="toast.info(t('statesShowcase.errRetrying'))"
              />
            </div>
            <div>
              <p class="subt">{{ t('statesShowcase.errAiSub') }}</p>
              <ErrorState
                variant="ai"
                :title="t('statesShowcase.errAiTitle')"
                :confidence="88"
              />
            </div>
          </div>
        </div>
      </section>

      <!-- ============================================================
           08 ConfirmDialog
      ============================================================ -->
      <section class="sec">
        <div class="sec__head">
          <span class="sec__no">08</span>
          <h2 class="sec__title">{{ t('statesShowcase.sec08Title') }}</h2>
          <span class="sec__spec">{{ t('statesShowcase.sec08Spec') }}</span>
        </div>
        <div class="sec__body">
          <div class="demos">
            <div class="dcell">
              <span class="vlab">{{ t('statesShowcase.confirmLabelSimple') }}</span>
              <AppButton variant="danger" @click="demoConfirmSimple">{{ t('statesShowcase.btnRollback126') }}</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">type-to-confirm</span>
              <AppButton variant="danger" @click="demoConfirmTypeToConfirm">{{ t('statesShowcase.confirmResetTitle') }}</AppButton>
            </div>
          </div>
          <p class="demo-hint">{{ t('statesShowcase.confirmHintPre') }} <code class="mono">acme</code> {{ t('statesShowcase.confirmHintPost') }}</p>
        </div>
      </section>

      <!-- ============================================================
           09 FormField
      ============================================================ -->
      <section class="sec">
        <div class="sec__head">
          <span class="sec__no">09</span>
          <h2 class="sec__title">{{ t('statesShowcase.sec09Title') }}</h2>
          <span class="sec__spec">{{ t('statesShowcase.sec09Spec') }}</span>
        </div>
        <div class="sec__body">
          <div class="form-demos">
            <FormField :label="t('statesShowcase.formDefaultLabel')" field-id="ff-default">
              <template #default="{ fieldId }">
                <input
                  :id="fieldId"
                  v-model="fieldDefault"
                  class="ui-demo-input"
                  type="text"
                  :placeholder="t('statesShowcase.formDefaultPlaceholder')"
                />
              </template>
            </FormField>

            <FormField
              :label="t('statesShowcase.formErrorLabel')"
              field-id="ff-error"
              :error="t('statesShowcase.formErrorMessage')"
            >
              <template #default="{ fieldId }">
                <input
                  :id="fieldId"
                  v-model="fieldError"
                  class="ui-demo-input ui-demo-input--error"
                  type="text"
                  aria-invalid="true"
                  aria-describedby="ff-error-err"
                />
              </template>
            </FormField>

            <FormField
              :label="t('statesShowcase.formHintLabel')"
              field-id="ff-hint"
              :hint="t('statesShowcase.formHintHint')"
            >
              <template #default="{ fieldId }">
                <input
                  :id="fieldId"
                  v-model="fieldHint"
                  class="ui-demo-input"
                  type="text"
                  :placeholder="t('statesShowcase.formHintPlaceholder')"
                />
              </template>
            </FormField>

            <FormField :label="t('statesShowcase.formDisabledLabel')" field-id="ff-disabled" :disabled="true">
              <template #default="{ fieldId }">
                <input
                  :id="fieldId"
                  v-model="fieldDisabled"
                  class="ui-demo-input"
                  type="text"
                  disabled
                />
              </template>
            </FormField>

            <FormField :label="t('statesShowcase.formRequiredLabel')" field-id="ff-required" :required="true">
              <template #default="{ fieldId }">
                <input
                  :id="fieldId"
                  class="ui-demo-input"
                  type="text"
                  :placeholder="t('statesShowcase.formRequiredPlaceholder')"
                />
              </template>
            </FormField>
          </div>
        </div>
      </section>

      <!-- ============================================================
           10 + 11: Tooltip + Progress in 2-col
      ============================================================ -->
      <div class="grid2">
        <!-- 10 Tooltip -->
        <section class="sec">
          <div class="sec__head">
            <span class="sec__no">10</span>
            <h2 class="sec__title">{{ t('statesShowcase.sec10Title') }}</h2>
            <span class="sec__spec">{{ t('statesShowcase.sec10Spec') }}</span>
          </div>
          <div class="sec__body">
            <div style="padding-top:40px;display:flex;gap:16px;flex-wrap:wrap;">
              <AppTooltip :content="t('statesShowcase.tooltipTerminalContent')">
                <AppButton variant="default">{{ t('statesShowcase.tooltipTerminalBtn') }}</AppButton>
              </AppTooltip>
              <AppTooltip :content="t('statesShowcase.tooltipBatchContent')" placement="top">
                <AppButton variant="primary">{{ t('statesShowcase.tooltipBatchBtn') }}</AppButton>
              </AppTooltip>
              <AppTooltip :content="t('statesShowcase.tooltipBottomContent')" placement="bottom">
                <AppButton variant="ghost">{{ t('statesShowcase.tooltipBottomBtn') }}</AppButton>
              </AppTooltip>
            </div>
          </div>
        </section>

        <!-- 11 Progress -->
        <section class="sec">
          <div class="sec__head">
            <span class="sec__no">11</span>
            <h2 class="sec__title">{{ t('statesShowcase.sec11Title') }}</h2>
            <span class="sec__spec">{{ t('statesShowcase.sec11Spec') }}</span>
          </div>
          <div class="sec__body" style="display:flex;flex-direction:column;gap:16px;">
            <div>
              <span class="vlab">{{ t('statesShowcase.progressDeterminate', { n: progressVal }) }}</span>
              <ProgressBar :value="progressVal" style="margin-top:6px;width:240px" :label="t('statesShowcase.progressDeployLabel')" />
              <input
                v-model.number="progressVal"
                type="range"
                min="0"
                max="100"
                style="margin-top:8px;width:240px;accent-color:var(--color-primary)"
                :aria-label="t('statesShowcase.progressAdjustAria')"
              />
            </div>
            <div>
              <span class="vlab">{{ t('statesShowcase.progressIndeterminate') }}</span>
              <ProgressBar variant="warn" style="margin-top:6px;width:240px" :label="t('statesShowcase.progressRunningLabel')" />
            </div>
            <div>
              <span class="vlab">{{ t('statesShowcase.progressSuccess') }}</span>
              <ProgressBar :value="100" variant="success" style="margin-top:6px;width:240px" :label="t('statesShowcase.progressDoneLabel')" />
            </div>
            <div>
              <span class="vlab">{{ t('statesShowcase.progressError') }}</span>
              <ProgressBar :value="38" variant="error" style="margin-top:6px;width:240px" :label="t('statesShowcase.progressFailLabel')" />
            </div>
          </div>
        </section>
      </div>

    </div><!-- /showcase-wrap -->

    <!-- ToastHost/ConfirmDialog 由 App.vue 全局挂载(模块级单例);此处不再自挂,
         否则 /states 下双挂导致 toast 渲染两份、确认对话框双焦点陷阱互殴。 -->

    <!-- Theme toggle -->
    <button
      class="showcase-theme-btn"
      type="button"
      :aria-label="themeStore.current === 'dark' ? t('statesShowcase.themeToLight') : t('statesShowcase.themeToDark')"
      @click="themeStore.toggle()"
    >
      {{ themeStore.current === 'dark' ? t('statesShowcase.themeLightLabel') : t('statesShowcase.themeDarkLabel') }}
    </button>
  </div>
</template>

<style scoped>
/* ——— Page layout ——— */
.showcase-page {
  min-height: 100vh;
  padding: 40px 48px 90px;
  font-family: var(--font-sans);
}

/* ——— Header ——— */
.showcase-head {
  max-width: 1180px;
  margin: 0 auto 26px;
}
.showcase-head__kk {
  display: flex;
  align-items: center;
  gap: 11px;
  margin-bottom: 8px;
}
.showcase-head__title {
  font-size: 1.5rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--color-text);
}
.showcase-head__tag {
  font-size: 0.68rem;
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  border-radius: 6px;
  padding: 3px 9px;
  font-weight: 600;
}
.showcase-head__desc {
  font-size: var(--text-body);
  color: var(--color-dim);
  max-width: 78ch;
  line-height: 1.6;
}

/* ——— Section wrapper ——— */
.showcase-wrap {
  max-width: 1180px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.sec {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: 16px;
  box-shadow: var(--shadow);
  overflow: hidden;
  animation: sec-in 0.5s var(--ease-out-expo) both;
}

@keyframes sec-in {
  from { opacity: 0; transform: translateY(13px); }
  to   { opacity: 1; transform: none; }
}

.sec__head {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 15px 20px;
  border-bottom: 1px solid var(--color-border);
}
.sec__no {
  font-family: var(--font-mono);
  font-size: 0.78rem;
  color: var(--color-faint);
  font-weight: 600;
}
.sec__title {
  font-size: 1rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  color: var(--color-text);
}
.sec__spec {
  margin-left: auto;
  font-size: 0.73rem;
  color: var(--color-faint);
  font-family: var(--font-mono);
  max-width: 54ch;
  text-align: right;
  line-height: 1.5;
}
.sec__body {
  padding: 20px;
}

/* ——— Grid ——— */
.grid2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 18px;
}
@media (max-width: 860px) {
  .grid2 { grid-template-columns: 1fr; }
}

/* ——— Demo atoms ——— */
.demos {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  align-items: flex-end;
}
.dcell {
  display: flex;
  flex-direction: column;
  gap: 7px;
}
.vlab {
  font-size: 0.69rem;
  color: var(--color-faint);
  font-family: var(--font-mono);
}
.subt {
  font-size: 0.74rem;
  color: var(--color-faint);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  margin-bottom: 11px;
}
.demo-hint {
  font-size: 0.76rem;
  color: var(--color-faint);
  margin-top: 12px;
  line-height: 1.5;
}

/* ——— Skeleton card wrapper ——— */
.sk-demo-card {
  border: 1px solid var(--color-border);
  border-radius: 13px;
  padding: 15px;
  background: var(--color-card-2);
  max-width: 300px;
}
.sk-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 14px;
}
.sk-col {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 7px;
}

/* ——— Form demo inputs (local to showcase) ——— */
.form-demos {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 18px;
}
.ui-demo-input {
  width: 100%;
  height: 38px;
  background: var(--color-inset);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  padding: 0 12px;
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: var(--text-body);
  transition:
    border-color var(--duration-fast),
    box-shadow var(--duration-fast);
}
.ui-demo-input::placeholder { color: var(--color-faint); }
.ui-demo-input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.ui-demo-input--error {
  border-color: var(--color-red);
  box-shadow: 0 0 0 3px var(--color-red-soft);
}
.ui-demo-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* ——— Theme toggle ——— */
.showcase-theme-btn {
  position: fixed;
  right: 20px;
  bottom: 18px;
  font-family: var(--font-sans);
  font-size: 0.74rem;
  color: var(--color-dim);
  border: 1px solid var(--color-border);
  background: var(--color-card);
  border-radius: var(--rounded);
  padding: 7px 13px;
  cursor: pointer;
  z-index: 200;
  box-shadow: var(--shadow);
  transition:
    color var(--duration-fast),
    border-color var(--duration-fast);
}
.showcase-theme-btn:hover { color: var(--color-text); border-color: var(--color-border-strong); }
.showcase-theme-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* ——— Mono utility ——— */
.mono { font-family: var(--font-mono); }

@media (prefers-reduced-motion: reduce) {
  .sec { animation: none; }
}
</style>
