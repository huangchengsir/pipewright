<script setup lang="ts">
import {ref,computed,onMounted} from 'vue'
import {useI18n} from 'vue-i18n'
import {getPromotionPlan,promoteRun,listRunPromotions,type PromotionPlanStage,type PromotionDTO,type PromotionStatus} from '../../api/promotion'
import {promotionStatusConfig,formatPromotionDate} from '../../api/promotion.helpers'
import {approveStage,rejectStage} from '../../api/runs'
import {HttpError} from '../../api/http'
import PermissionGate from '../PermissionGate.vue'
import {usePermission} from '../../composables/usePermission'
import {useAuthorizedRequestScope} from '../../composables/useAuthorizedRequestScope'
const props=defineProps<{runId:string;projectId:string;runStatus:string}>()
const emit=defineEmits<{(e:'promotion-pending'):void}>()
const {t}=useI18n()
const chain=ref<PromotionPlanStage[]>([]),chainLoaded=ref(false),configured=ref(false),atTop=ref(false),nextEnv=ref<PromotionPlanStage|null>(null)
const promotions=ref<PromotionDTO[]>([]),historyLoading=ref(false),historyError=ref(''),promotionError=ref('')
const deciding=ref(false),promoting=ref(false),promoted=ref(false),showConfirm=ref(false)
const scope=useAuthorizedRequestScope({action:'run.read',resourceId:props.runId},()=>{chain.value=[];promotions.value=[];nextEnv.value=null;chainLoaded.value=false;showConfirm.value=false;promotionError.value='';historyError.value=''})
const pendingPromotion=computed(()=>promotions.value.find(p=>p.status==='pending')??null)
const isNextGated=computed(()=>nextEnv.value?.gated??false)
const promoteQuery=computed(()=>({action:'release.promote',resourceId:props.runId,environmentId:nextEnv.value?.environmentId}))
const approvalQuery=computed(()=>({action:'release.approve',resourceId:props.runId,environmentId:chain.value.find(e=>e.name===pendingPromotion.value?.targetEnvironment)?.environmentId}))
const promotePermission=usePermission(promoteQuery),approvalPermission=usePermission(approvalQuery)
async function loadHistory():Promise<void>{
 if(!scope.active())return;historyLoading.value=true;historyError.value=''
 try{const [history,plan]=await scope.run(()=>Promise.all([listRunPromotions(props.runId,scope.requestOptions),getPromotionPlan(props.runId,scope.requestOptions)]));promotions.value=history.items;chain.value=plan.environments;nextEnv.value=plan.next??null;configured.value=plan.configured;atTop.value=plan.atTop;chainLoaded.value=true}
 catch(error){if(!scope.active())return;promotions.value=[];chain.value=[];nextEnv.value=null;historyError.value=error instanceof HttpError?(error.apiError?.message??t('run.loadHistoryFailed',{status:error.status})):t('run.promoteRequestFailed')}
 finally{if(scope.active())historyLoading.value=false}
}
function mapPromoteError(err:unknown):string{
 if(!(err instanceof HttpError))return t('run.promoteRequestFailed')
 const key:Record<string,string>={chain_not_configured:'promoteErrChainNotConfigured',run_not_successful:'promoteErrRunNotSuccessful',already_promoted:'promoteErrAlreadyPromoted',already_at_top:'promoteErrAlreadyAtTop',skip_env:'promoteErrSkipEnv',unknown_env:'promoteErrUnknownEnv'}
 if(err.apiError?.code==='gate_rejected')return ''
 return key[err.apiError?.code??'']?t('run.'+key[err.apiError!.code]):(err.apiError?.message??t('run.promoteRequestFailed'))
}
function requestPromote(){if(!scope.active()||!promotePermission.allowed.value)return;if(isNextGated.value)showConfirm.value=true;else void doPromote()}
function cancelConfirm(){showConfirm.value=false}
async function doPromote(){
 if(!scope.active()||!promotePermission.allowed.value||!nextEnv.value||promoting.value)return
 if(nextEnv.value.gated&&!showConfirm.value)return
 const target=nextEnv.value.name,gated=nextEnv.value.gated;showConfirm.value=false;promoting.value=true;promotionError.value='';promoted.value=false
 if(gated){
  void scope.run(()=>promoteRun(props.runId,{targetEnvironment:target},scope.requestOptions)).then(async()=>{if(scope.active())await loadHistory()},async error=>{if(!scope.active())return;promotionError.value=mapPromoteError(error);await loadHistory()})
  emit('promotion-pending');await loadHistory();if(scope.active()&&!pendingPromotion.value){await new Promise(resolve=>setTimeout(resolve,300));await loadHistory()}
  if(scope.active())promoting.value=false;return
 }
 try{const dto=await scope.run(()=>promoteRun(props.runId,{targetEnvironment:target},scope.requestOptions));promoted.value=dto.status==='promoted';await loadHistory()}
 catch(error){if(scope.active())promotionError.value=mapPromoteError(error)}
 finally{if(scope.active())promoting.value=false}
}
async function decidePromotion(approve:boolean){
 const target=pendingPromotion.value?.targetEnvironment;if(!target||deciding.value||!scope.active()||!approvalPermission.allowed.value)return
 deciding.value=true;promotionError.value=''
 try{await scope.run(()=>approve?approveStage(props.runId,`promote:${target}`,scope.requestOptions):rejectStage(props.runId,`promote:${target}`,scope.requestOptions));await loadHistory();for(let i=0;i<5&&scope.active()&&pendingPromotion.value;i++){await new Promise(resolve=>setTimeout(resolve,200));await loadHistory()}}
 catch(error){if(scope.active())promotionError.value=error instanceof HttpError?(error.apiError?.message??t('run.approveFailed',{status:error.status})):t('run.approveFailedRetry')}
 finally{if(scope.active())deciding.value=false}
}
const statusCfg=(status:PromotionStatus)=>promotionStatusConfig(status)
const fmtDate=(iso:string)=>formatPromotionDate(iso)
onMounted(loadHistory)
</script>
<template>
  <div class="promotion-panel" role="region" aria-labelledby="promo-heading">

    <!-- ─── Promote action section ──────────────────────────────────────── -->
    <div class="promo-action-card">
      <div class="promo-action-head">
        <div class="promo-icon" aria-hidden="true">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M12 2 2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
          </svg>
        </div>
        <h3 id="promo-heading" class="promo-title">{{ t('run.promotionEnv') }}</h3>
        <span v-if="chain.length > 0" class="promo-chain-badge" :aria-label="t('run.envChainAria')">
          {{ chain.map(e => e.name).join(' → ') }}
        </span>
      </div>

      <!-- Chain not loaded / empty -->
      <div v-if="chainLoaded && !configured" class="promo-empty">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
        </svg>
        <span>{{ t('run.chainNotConfigured') }}</span>
      </div>

      <!-- Pending gated promotion: 审批门批准/拒绝就在这里(优先于「已到链尾」,因 pending 也算到顶) -->
      <div v-else-if="pendingPromotion" class="promo-action-body">
        <div class="promo-banner promo-banner--pending" role="status" aria-live="polite">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
          </svg>
          {{ t('run.promotePendingApproval', { env: pendingPromotion.targetEnvironment }) }}
        </div>
        <div v-if="promotionError" class="promo-banner promo-banner--error" role="alert" aria-live="assertive">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          {{ promotionError }}
        </div>
        <div class="promo-btn-row">
          <PermissionGate :query="approvalQuery"><button
            class="promo-btn promo-btn--gate"
            :disabled="deciding"
            :aria-busy="deciding"
            @click="decidePromotion(true)"
          >
            <span v-if="deciding" class="promo-spinner" aria-hidden="true" />
            {{ deciding ? t('run.processing') : t('run.approvePromoteTo', { env: pendingPromotion.targetEnvironment }) }}
          </button></PermissionGate>
          <PermissionGate :query="approvalQuery"><button class="promo-btn promo-btn--cancel" :disabled="deciding" @click="decidePromotion(false)">
            {{ t('run.reject') }}
          </button></PermissionGate>
        </div>
      </div>

      <!-- Already at top -->
      <div
        v-else-if="chainLoaded && atTop && promotions.length > 0"
        class="promo-empty promo-empty--top"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
          <path d="M20 6 9 17l-5-5"/>
        </svg>
        <span>{{ t('run.atTopEnv', { env: chain[chain.length - 1]?.name }) }}</span>
      </div>

      <!-- Promote button -->
      <div v-else-if="chainLoaded && nextEnv !== null && props.runStatus === 'success'" class="promo-action-body">
        <!-- Success flash -->
        <div v-if="promoted" class="promo-banner promo-banner--success" role="status" aria-live="polite">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
            <path d="M20 6 9 17l-5-5"/>
          </svg>
          {{ t('run.promoteSuccess', { env: promotions[0]?.targetEnvironment }) }}
        </div>

        <!-- Pending gate banner -->
        <div
          v-if="promotions[0]?.status === 'pending'"
          class="promo-banner promo-banner--pending"
          role="status"
          aria-live="polite"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
          </svg>
          {{ t('run.promotePendingGate', { env: promotions[0]?.targetEnvironment }) }}
        </div>

        <!-- Error banner -->
        <div v-if="promotionError" class="promo-banner promo-banner--error" role="alert" aria-live="assertive">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          {{ promotionError }}
        </div>

        <!-- Gated confirm dialog -->
        <div v-if="showConfirm" class="promo-confirm" role="alertdialog" aria-modal="true" aria-labelledby="promo-confirm-title">
          <div class="promo-confirm-icon" aria-hidden="true">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
              <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
            </svg>
          </div>
          <div class="promo-confirm-body">
            <p id="promo-confirm-title" class="promo-confirm-title">
              {{ t('run.promoteGatedConfirmTitle', { env: nextEnv?.name }) }}
            </p>
            <p class="promo-confirm-sub">
              {{ t('run.promoteGatedConfirmSub') }}
            </p>
          </div>
          <div class="promo-confirm-actions">
            <button class="promo-btn promo-btn--cancel" @click="cancelConfirm">{{ t('run.cancel') }}</button>
            <PermissionGate :query="promoteQuery"><button class="promo-btn promo-btn--gate" :disabled="promoting" :aria-busy="promoting" @click="doPromote">
              <span v-if="promoting" class="promo-spinner" aria-hidden="true" />
              {{ promoting ? t('run.submitting') : t('run.submitPromoteRequest') }}
            </button></PermissionGate>
          </div>
        </div>

        <!-- Main promote button -->
        <div v-else class="promo-btn-row">
          <PermissionGate :query="promoteQuery"><button
            class="promo-btn promo-btn--promote"
            :disabled="promoting || promotions[0]?.status === 'pending'"
            :aria-busy="promoting"
            @click="requestPromote"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M12 2 2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
            </svg>
            <span v-if="promoting" class="promo-spinner" aria-hidden="true" />
            {{
              promoting
                ? t('run.promoting')
                : isNextGated
                  ? t('run.applyPromoteGated', { env: nextEnv?.name })
                  : t('run.promoteTo', { env: nextEnv?.name })
            }}
          </button></PermissionGate>
          <span v-if="isNextGated" class="promo-gated-hint">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
            </svg>
            {{ t('run.gateNeedsApproval') }}
          </span>
        </div>
      </div>

      <!-- Run not successful -->
      <div v-else-if="props.runStatus !== 'success'" class="promo-empty">
        <span>{{ t('run.onlySuccessfulPromote') }}</span>
      </div>

      <!-- Loading -->
      <div v-else-if="!chainLoaded" class="promo-loading" aria-busy="true" :aria-label="t('run.loadingChainAria')">
        <div class="promo-skel promo-skel--btn" aria-hidden="true" />
      </div>
    </div>

    <!-- ─── Promotion history ────────────────────────────────────────────── -->
    <div class="promo-history" aria-labelledby="promo-hist-heading">
      <div class="promo-history-head">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9"/><path d="M12 6v6l4 2"/>
        </svg>
        <h4 id="promo-hist-heading" class="promo-history-title">{{ t('run.promotionHistory') }}</h4>
        <span class="promo-hist-count">{{ promotions.length }}</span>
      </div>

      <div v-if="historyLoading" class="promo-hist-body promo-hist-loading" aria-busy="true" :aria-label="t('run.loadingHistoryAria')">
        <div class="promo-skel promo-skel--row" aria-hidden="true" />
        <div class="promo-skel promo-skel--row" aria-hidden="true" />
      </div>

      <div v-else-if="historyError" class="promo-hist-body promo-hist-error" role="alert">
        {{ historyError }}
      </div>

      <div v-else-if="promotions.length === 0" class="promo-hist-body promo-hist-empty">
        <span>{{ t('run.noPromotionRecords') }}</span>
      </div>

      <ol v-else class="promo-hist-list" :aria-label="t('run.historyListAria')">
        <li
          v-for="p in promotions"
          :key="p.id"
          class="promo-hist-item"
          :aria-label="t('run.historyItemAria', { env: p.targetEnvironment, status: statusCfg(p.status).label, date: fmtDate(p.createdAt) })"
        >
          <!-- Status dot -->
          <span
            class="hist-dot"
            :style="{ background: statusCfg(p.status).color }"
            aria-hidden="true"
          />

          <!-- Arrow indicator -->
          <div class="hist-route">
            <span v-if="p.fromEnvironment" class="hist-from mono">{{ p.fromEnvironment }}</span>
            <svg v-if="p.fromEnvironment" width="12" height="8" viewBox="0 0 12 8" fill="none" aria-hidden="true">
              <path d="M0 4h10M7 1l3 3-3 3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            <span class="hist-to mono">{{ p.targetEnvironment }}</span>
          </div>

          <!-- Status badge -->
          <span
            class="hist-status"
            :style="{
              color: statusCfg(p.status).color,
              background: statusCfg(p.status).bg,
              borderColor: statusCfg(p.status).border,
            }"
          >{{ statusCfg(p.status).label }}</span>

          <!-- Meta -->
          <div class="hist-meta">
            <span class="hist-by">{{ p.promotedBy }}</span>
            <span class="hist-sep" aria-hidden="true">·</span>
            <time :datetime="p.createdAt" class="hist-time">{{ fmtDate(p.createdAt) }}</time>
          </div>
        </li>
      </ol>
    </div>

  </div>
</template>

<style scoped>
.promotion-panel {
  display: flex;
  flex-direction: column;
  gap: 10px;
  border-radius: var(--rounded);
  border: 1px solid var(--color-border);
  overflow: hidden;
  background: var(--color-card);
}

/* ─── Action card ─────────────────────────────────── */
.promo-action-card {
  padding: 0;
}

.promo-action-head {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 13px 16px 11px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-inset);
}

.promo-icon {
  width: 22px;
  height: 22px;
  border-radius: 6px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.promo-title {
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-text);
}

.promo-chain-badge {
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--color-faint);
  background: var(--color-card-2);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
  padding: 2px 8px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 300px;
}

.promo-action-body {
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.promo-empty {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 14px 16px;
  font-size: 0.81rem;
  color: var(--color-faint);
}

.promo-empty--top {
  color: var(--color-green);
}

.promo-loading {
  padding: 14px 16px;
}

/* ─── Banners ─────────────────────────────────────── */
.promo-banner {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 13px;
  border-radius: var(--rounded-md);
  font-size: 0.82rem;
  line-height: 1.5;
}

.promo-banner--success {
  background: var(--color-green-soft);
  border: 1px solid var(--color-green-line);
  color: var(--color-green);
}

.promo-banner--pending {
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  color: var(--color-amber);
}

.promo-banner--error {
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  color: var(--color-red);
}

/* ─── Confirm dialog (inline) ─────────────────────── */
.promo-confirm {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px;
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  border-radius: var(--rounded);
}

.promo-confirm-icon {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  border-radius: var(--rounded-md);
  background: var(--color-amber);
  color: #fff;
  display: grid;
  place-items: center;
}

.promo-confirm-body {
  flex: 1;
  min-width: 0;
}

.promo-confirm-title {
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-text);
  line-height: 1.4;
}

.promo-confirm-sub {
  margin-top: 3px;
  font-size: 0.78rem;
  color: var(--color-dim);
  line-height: 1.5;
}

.promo-confirm-actions {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-shrink: 0;
}

/* ─── Buttons ─────────────────────────────────────── */
.promo-btn-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.promo-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 36px;
  padding: 0 16px;
  border: none;
  font-family: var(--font-sans);
  font-size: 0.84rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  white-space: nowrap;
  transition: filter var(--duration-fast), background-color var(--duration-fast), opacity var(--duration-fast);
}

.promo-btn--promote {
  background: var(--color-primary);
  color: #fff;
  box-shadow: 0 4px 14px var(--color-primary-soft);
}
.promo-btn--promote:hover:not(:disabled) { filter: brightness(1.08); }
.promo-btn--promote:disabled { opacity: 0.45; cursor: not-allowed; box-shadow: none; }

.promo-btn--gate {
  background: var(--color-amber);
  color: #fff;
}
.promo-btn--gate:hover:not(:disabled) { filter: brightness(1.06); }
.promo-btn--gate:disabled { opacity: 0.5; cursor: not-allowed; }

.promo-btn--cancel {
  background: var(--color-card-2);
  color: var(--color-text);
  border: 1px solid var(--color-border-strong);
}
.promo-btn--cancel:hover { border-color: var(--color-faint); }

.promo-gated-hint {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 0.75rem;
  color: var(--color-amber);
}

.promo-spinner {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 2px solid rgba(255, 255, 255, 0.3);
  border-top-color: #fff;
  border-radius: var(--rounded-full);
  animation: promo-spin 0.7s linear infinite;
  flex-shrink: 0;
}
@keyframes promo-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .promo-spinner { animation: none; border-top-color: currentColor; } }

/* ─── Skeleton ────────────────────────────────────── */
.promo-skel {
  display: block;
  background: linear-gradient(90deg, var(--color-inset) 0%, oklch(100% 0 0 / 0.06) 50%, var(--color-inset) 100%);
  background-size: 200% 100%;
  border-radius: var(--rounded-md);
  animation: promo-shimmer 1.4s ease-in-out infinite;
}
@keyframes promo-shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}
@media (prefers-reduced-motion: reduce) { .promo-skel { animation: none; background: var(--color-inset); } }
.promo-skel--btn { height: 36px; width: 220px; border-radius: var(--rounded); }
.promo-skel--row { height: 32px; width: 100%; margin-bottom: 6px; }

/* ─── History section ─────────────────────────────── */
.promo-history {
  border-top: 1px solid var(--color-border);
}

.promo-history-head {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 10px 16px;
  background: var(--color-inset);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-dim);
}

.promo-history-title {
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--color-text);
  flex: 1;
}

.promo-hist-count {
  font-size: 0.7rem;
  font-weight: 700;
  padding: 1px 7px;
  border-radius: var(--rounded-full);
  background: var(--color-border-strong);
  color: var(--color-dim);
}

.promo-hist-body {
  padding: 14px 16px;
}

.promo-hist-loading {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.promo-hist-error {
  font-size: 0.8rem;
  color: var(--color-red);
}

.promo-hist-empty {
  font-size: 0.8rem;
  color: var(--color-faint);
  font-style: italic;
}

/* History list */
.promo-hist-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 0;
}

.promo-hist-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--color-border);
  transition: background-color var(--duration-fast);
}

.promo-hist-item:last-child { border-bottom: none; }
.promo-hist-item:hover { background: var(--color-inset); }

.hist-dot {
  width: 7px;
  height: 7px;
  border-radius: var(--rounded-full);
  flex-shrink: 0;
}

.hist-route {
  display: flex;
  align-items: center;
  gap: 5px;
  flex: 1;
  min-width: 0;
}

.hist-from {
  font-size: 0.78rem;
  color: var(--color-faint);
}

.hist-to {
  font-size: 0.82rem;
  font-weight: 500;
  color: var(--color-text);
}

.hist-status {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: var(--rounded-md);
  border: 1px solid transparent;
  font-size: 0.72rem;
  font-weight: 600;
  white-space: nowrap;
  letter-spacing: 0.02em;
  flex-shrink: 0;
}

.hist-meta {
  display: flex;
  align-items: center;
  gap: 5px;
  flex-shrink: 0;
  font-size: 0.74rem;
  color: var(--color-faint);
}

.hist-sep { color: var(--color-border-strong); }
.hist-by { white-space: nowrap; max-width: 90px; overflow: hidden; text-overflow: ellipsis; }
.hist-time { white-space: nowrap; }

.mono { font-family: var(--font-mono); }
</style>
