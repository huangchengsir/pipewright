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
import { useThemeStore } from '../stores/theme'
import { useToast } from '../composables/useToast'
import { useConfirm } from '../composables/useConfirm'
import StatusBadge   from '../components/ui/StatusBadge.vue'
import AppButton     from '../components/ui/AppButton.vue'
import AppBanner     from '../components/ui/AppBanner.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'
import EmptyState    from '../components/ui/EmptyState.vue'
import ErrorState    from '../components/ui/ErrorState.vue'
import ConfirmDialog from '../components/ui/ConfirmDialog.vue'
import ToastHost     from '../components/ui/ToastHost.vue'
import FormField     from '../components/ui/FormField.vue'
import AppTooltip   from '../components/ui/AppTooltip.vue'
import ProgressBar   from '../components/ui/ProgressBar.vue'

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
  toast.success('部署成功', { detail: 'acme-web #127 已上线 生产-1' })
}
function fireErrorToast() {
  toast.error('部署失败', {
    detail: '#128 健康检查超时,已回滚',
    action: { label: '查看 AI 诊断', onClick: () => console.log('AI diag') },
  })
}
function fireWarnToast() {
  toast.warn('磁盘偏高', { detail: '生产-1 磁盘 91%' })
}
function fireInfoToast() {
  toast.info('AI 诊断已生成', {
    detail: '#128 根因:DB_PASSWORD 未注入',
    action: { label: '查看', onClick: () => console.log('view') },
  })
}

// ——— Section 08: Confirm demos ———
async function demoConfirmSimple() {
  const ok = await confirm.open({
    title: '回滚到 #126?',
    body: '将把 生产-1 的 acme-web 切回上一稳定版本 #126,当前 #128 实例会被停止。',
    confirmLabel: '确认回滚',
    variant: 'danger',
  })
  toast[ok ? 'success' : 'info'](ok ? '已确认回滚' : '已取消')
}

async function demoConfirmTypeToConfirm() {
  const ok = await confirm.open({
    title: '重置实例',
    body: '将销毁所有项目、服务器与凭据保险库,不可恢复。',
    confirmText: 'acme',
    confirmLabel: '永久重置',
    variant: 'danger',
  })
  toast[ok ? 'success' : 'info'](ok ? '已确认重置' : '已取消')
}

// ——— Section 09: Form fields ———
const fieldDefault = ref('')
const fieldError   = ref('non-valid-url')
const fieldHint    = ref('')
const fieldDisabled = ref('不可编辑')

// ——— Section 11: Progress ———
const progressVal = ref(55)
</script>

<template>
  <div class="showcase-page">
    <!-- Page header -->
    <header class="showcase-head">
      <div class="showcase-head__kk">
        <h1 class="showcase-head__title">组件与状态规范</h1>
        <span class="showcase-head__tag">前端交接 · UI States</span>
      </div>
      <p class="showcase-head__desc">
        所有界面通用的状态与组件,统一在此定义。
        <strong>颜色一律走 DESIGN.md 令牌</strong>(绿=成功、红=失败、琥珀=进行中/警告、青=AI/信息、蓝=主操作),
        动效仅用 <code class="mono">transform/opacity</code> 并遵守 <code class="mono">prefers-reduced-motion</code>。
      </p>
    </header>

    <div class="showcase-wrap">

      <!-- ============================================================
           01 StatusBadge
      ============================================================ -->
      <section class="sec">
        <div class="sec__head">
          <span class="sec__no">01</span>
          <h2 class="sec__title">状态徽标(运行/服务/项目通用词汇)</h2>
          <span class="sec__spec">语义固定,勿换词 · 进行中脉冲 · 色+圆点+文字三维度区分</span>
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
          <h2 class="sec__title">按钮 · 层级与交互态</h2>
          <span class="sec__spec">主按钮带 --primary-soft 阴影 · hover 上浮 1px · loading 时禁点并显 spinner</span>
        </div>
        <div class="sec__body">
          <p class="subt">层级</p>
          <div class="demos">
            <div class="dcell">
              <span class="vlab">primary</span>
              <AppButton variant="primary">重新部署</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">default</span>
              <AppButton variant="default">查看日志</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">ghost</span>
              <AppButton variant="ghost">取消</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">danger</span>
              <AppButton variant="danger">回滚到 #126</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">ai</span>
              <AppButton variant="ai">AI 诊断</AppButton>
            </div>
          </div>

          <p class="subt" style="margin-top:18px">primary 各态</p>
          <div class="demos">
            <div class="dcell">
              <span class="vlab">default</span>
              <AppButton variant="primary">默认</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">loading</span>
              <AppButton variant="primary" :loading="loadingPrimary" @click="demoLoadPrimary">
                {{ loadingPrimary ? '提交中…' : '点击加载' }}
              </AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">disabled</span>
              <AppButton variant="primary" disabled>禁用</AppButton>
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
          <h2 class="sec__title">Toast 通知</h2>
          <span class="sec__spec">右下角堆叠 · 成功/信息 4s 自动消 · 错误手动关 · 可带行动按钮</span>
        </div>
        <div class="sec__body">
          <div class="demos">
            <div class="dcell">
              <span class="vlab">success (4s)</span>
              <AppButton variant="default" @click="fireSuccessToast">触发 success</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">error (手动关)</span>
              <AppButton variant="danger" @click="fireErrorToast">触发 error</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">warn (6s)</span>
              <AppButton variant="default" @click="fireWarnToast">触发 warn</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">info + action (4s)</span>
              <AppButton variant="ai" @click="fireInfoToast">触发 info</AppButton>
            </div>
          </div>
          <p class="demo-hint">Toast 显示在右下角,可堆叠。点击上方按钮逐一触发。</p>
        </div>
      </section>

      <!-- ============================================================
           04 AppBanner
      ============================================================ -->
      <section class="sec">
        <div class="sec__head">
          <span class="sec__no">04</span>
          <h2 class="sec__title">内联提示 Banner</h2>
          <span class="sec__spec">页面/卡片内常驻提示,不自动消失 · 用对应语义 *-soft 底 + *-line 边</span>
        </div>
        <div class="sec__body" style="display:flex;flex-direction:column;gap:10px;">
          <AppBanner variant="info">
            首次部署无可对比基线,差异视图暂不可用。
            <template #action>了解 diff →</template>
          </AppBanner>

          <AppBanner variant="warn" title="未配置健康检查">
            容器启动成功即视为部署成功。
          </AppBanner>

          <AppBanner variant="error" title="凭据错误">
            拉取仓库失败,请检查 Gitee 访问令牌。
            <template #action>前往凭据 →</template>
          </AppBanner>

          <AppBanner variant="success">
            配置校验通过,可保存并触发运行。
          </AppBanner>

          <AppBanner variant="ai" title="AI 功能">
            当前使用 Claude claude-sonnet-4-6 模型 · 诊断速度高。
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
            <h2 class="sec__title">加载骨架</h2>
            <span class="sec__spec">数据加载用骨架,不用转圈遮罩 · shimmer 1.4s</span>
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
            <h2 class="sec__title">空状态</h2>
            <span class="sec__spec">图标 + 一句说明 + 明确 CTA</span>
          </div>
          <div class="sec__body">
            <EmptyState
              title="还没有项目"
              description="接入第一个 Gitee 仓库,AI 会分析代码帮你生成流水线配置。"
            >
              <template #cta>
                <AppButton variant="primary">+ 新建项目</AppButton>
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
          <h2 class="sec__title">错误态</h2>
          <span class="sec__spec">字段内联红 + 整页加载失败可重试 + AI 不可用优雅降级(不阻断 CI/CD)</span>
        </div>
        <div class="sec__body">
          <div class="grid2">
            <div>
              <p class="subt">整页加载失败</p>
              <ErrorState
                title="加载运行记录失败"
                description="无法连接到 生产-1(SSH 超时)"
                @retry="toast.info('重试中…')"
              />
            </div>
            <div>
              <p class="subt">AI 诊断降级</p>
              <ErrorState
                variant="ai"
                title="AI 失败诊断"
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
          <h2 class="sec__title">二次确认(破坏性操作)</h2>
          <span class="sec__spec">回滚/删除/重启需确认 · 高危(重置/删服务器)要求输入名称确认 · Esc 关 · 焦点陷阱</span>
        </div>
        <div class="sec__body">
          <div class="demos">
            <div class="dcell">
              <span class="vlab">普通确认</span>
              <AppButton variant="danger" @click="demoConfirmSimple">回滚到 #126</AppButton>
            </div>
            <div class="dcell">
              <span class="vlab">type-to-confirm</span>
              <AppButton variant="danger" @click="demoConfirmTypeToConfirm">重置实例</AppButton>
            </div>
          </div>
          <p class="demo-hint">点击按钮打开对话框 · Esc 关 · type-to-confirm 版需输入 <code class="mono">acme</code> 才可确认</p>
        </div>
      </section>

      <!-- ============================================================
           09 FormField
      ============================================================ -->
      <section class="sec">
        <div class="sec__head">
          <span class="sec__no">09</span>
          <h2 class="sec__title">表单控件 · 各态</h2>
          <span class="sec__spec">focus = --primary 边 + 3px soft 光环 · 错误 aria-describedby 关联</span>
        </div>
        <div class="sec__body">
          <div class="form-demos">
            <FormField label="默认输入" field-id="ff-default">
              <template #default="{ fieldId }">
                <input
                  :id="fieldId"
                  v-model="fieldDefault"
                  class="ui-demo-input"
                  type="text"
                  placeholder="占位文本"
                />
              </template>
            </FormField>

            <FormField
              label="错误态"
              field-id="ff-error"
              error="Webhook 地址格式不正确"
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
              label="带提示"
              field-id="ff-hint"
              hint="填写后将用于 AI 分析"
            >
              <template #default="{ fieldId }">
                <input
                  :id="fieldId"
                  v-model="fieldHint"
                  class="ui-demo-input"
                  type="text"
                  placeholder="例:生产环境"
                />
              </template>
            </FormField>

            <FormField label="禁用态" field-id="ff-disabled" :disabled="true">
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

            <FormField label="必填" field-id="ff-required" :required="true">
              <template #default="{ fieldId }">
                <input
                  :id="fieldId"
                  class="ui-demo-input"
                  type="text"
                  placeholder="必填字段"
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
            <h2 class="sec__title">Tooltip</h2>
            <span class="sec__spec">暗底 · 出现在元素上方 · 仅放简短补充 · 键盘可达</span>
          </div>
          <div class="sec__body">
            <div style="padding-top:40px;display:flex;gap:16px;flex-wrap:wrap;">
              <AppTooltip content="经 SSH → docker exec,操作纳入审计">
                <AppButton variant="default">›_ 进入容器终端</AppButton>
              </AppTooltip>
              <AppTooltip content="将在所有选中环境同步执行" placement="top">
                <AppButton variant="primary">批量部署</AppButton>
              </AppTooltip>
              <AppTooltip content="底部 tooltip" placement="bottom">
                <AppButton variant="ghost">底部示例</AppButton>
              </AppTooltip>
            </div>
          </div>
        </section>

        <!-- 11 Progress -->
        <section class="sec">
          <div class="sec__head">
            <span class="sec__no">11</span>
            <h2 class="sec__title">进度 / Spinner</h2>
            <span class="sec__spec">确定进度用条 · 不确定用流光 · 颜色走语义 token</span>
          </div>
          <div class="sec__body" style="display:flex;flex-direction:column;gap:16px;">
            <div>
              <span class="vlab">确定 · {{ progressVal }}%</span>
              <ProgressBar :value="progressVal" style="margin-top:6px;width:240px" label="部署进度" />
              <input
                v-model.number="progressVal"
                type="range"
                min="0"
                max="100"
                style="margin-top:8px;width:240px;accent-color:var(--color-primary)"
                aria-label="调节进度值"
              />
            </div>
            <div>
              <span class="vlab">不确定(运行中) — warn</span>
              <ProgressBar variant="warn" style="margin-top:6px;width:240px" label="运行中" />
            </div>
            <div>
              <span class="vlab">成功</span>
              <ProgressBar :value="100" variant="success" style="margin-top:6px;width:240px" label="已完成" />
            </div>
            <div>
              <span class="vlab">错误</span>
              <ProgressBar :value="38" variant="error" style="margin-top:6px;width:240px" label="失败进度" />
            </div>
          </div>
        </section>
      </div>

    </div><!-- /showcase-wrap -->

    <!-- Global overlays (mount here for showcase page) -->
    <ToastHost />
    <ConfirmDialog />

    <!-- Theme toggle -->
    <button
      class="showcase-theme-btn"
      type="button"
      :aria-label="themeStore.current === 'dark' ? '切换到浅色' : '切换到深色'"
      @click="themeStore.toggle()"
    >
      {{ themeStore.current === 'dark' ? '◐ 浅色' : '◑ 深色' }}
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
