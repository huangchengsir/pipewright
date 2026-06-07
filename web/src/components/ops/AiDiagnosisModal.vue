<script setup lang="ts">
/*
  AiDiagnosisModal.vue — 容器 AI 诊断 / 看日志。取容器最近日志 → ai.Diagnose,
  展示根因假说 + 置信度 + 修复建议 + 可粘贴修复脚本 + 证据行。AI 未配/失败优雅降级。
*/
import { ref } from 'vue'
import { diagnoseContainer, type ContainerDiagnosis } from '../../api/containers'
import { HttpError } from '../../api/http'
import { useToast } from '../../composables/useToast'

const props = defineProps<{ serverId: string; containerName: string }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const toast = useToast()
const state = ref<'loading' | 'done' | 'error'>('loading')
const errorMsg = ref('')
const diag = ref<ContainerDiagnosis | null>(null)

const CONFIDENCE_LABEL: Record<string, string> = { high: '高', medium: '中', low: '低', '': '' }

async function run(): Promise<void> {
  state.value = 'loading'
  errorMsg.value = ''
  try {
    diag.value = await diagnoseContainer(props.serverId, props.containerName)
    state.value = 'done'
  } catch (err) {
    state.value = 'error'
    errorMsg.value =
      err instanceof HttpError ? (err.apiError?.message ?? `诊断失败(${err.status})`) : '诊断请求失败'
  }
}

async function copyScript(): Promise<void> {
  if (!diag.value?.fixScript) return
  try {
    await navigator.clipboard.writeText(diag.value.fixScript)
    toast.success('修复脚本已复制')
  } catch {
    toast.error('复制失败', { detail: '需 https/localhost' })
  }
}

void run()
</script>

<template>
  <div class="modal-scrim" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-label="AI 容器诊断">
      <header class="modal__head">
        <h3 class="modal__title">
          <span class="ai-spark">✦</span> AI 诊断 · <span class="mono">{{ containerName }}</span>
        </h3>
        <button class="modal__close" aria-label="关闭" @click="emit('close')">✕</button>
      </header>

      <div class="modal__body">
        <!-- 加载中 -->
        <div v-if="state === 'loading'" class="center">
          <span class="spinner" /> 正在取日志并让 AI 分析…
        </div>

        <!-- 请求错误 -->
        <div v-else-if="state === 'error'" class="center err">⚠ {{ errorMsg }}</div>

        <!-- AI 未配 / 不可用 -->
        <div v-else-if="diag && diag.status !== 'ready'" class="unavail">
          <p class="unavail__t">诊断暂不可用</p>
          <p class="unavail__r">{{ diag.reason }}</p>
        </div>

        <!-- 诊断结果 -->
        <template v-else-if="diag">
          <section class="block">
            <div class="block__h">
              根因假说
              <span v-if="diag.confidence" class="conf" :class="`conf--${diag.confidence}`">
                置信度 {{ CONFIDENCE_LABEL[diag.confidence] }}
              </span>
            </div>
            <p class="hyp">{{ diag.hypothesis }}</p>
          </section>

          <section v-if="diag.fixSuggestions.length" class="block">
            <div class="block__h">修复建议</div>
            <ul class="fixlist">
              <li v-for="(f, i) in diag.fixSuggestions" :key="i">{{ f }}</li>
            </ul>
          </section>

          <section v-if="diag.fixScript" class="block">
            <div class="block__h">
              修复脚本
              <button class="copy" @click="copyScript">复制</button>
            </div>
            <pre class="script mono">{{ diag.fixScript }}</pre>
          </section>

          <section v-if="diag.alternateCauses.length" class="block">
            <div class="block__h">其它可能原因</div>
            <ul class="fixlist fixlist--dim">
              <li v-for="(c, i) in diag.alternateCauses" :key="i">{{ c }}</li>
            </ul>
          </section>

          <section v-if="diag.evidence.length" class="block">
            <div class="block__h">日志证据</div>
            <pre class="evidence mono"><span
              v-for="(e, i) in diag.evidence"
              :key="i"
              class="evline"
              :class="{ 'evline--hl': e.highlight }"
            >{{ e.text }}
</span></pre>
          </section>
        </template>
      </div>

      <footer class="modal__foot">
        <span class="foot-hint">日志出网前已脱敏 · AI 仅供参考</span>
        <span class="grow" />
        <button class="btn btn--ghost" @click="run">重新诊断</button>
        <button class="btn btn--primary" @click="emit('close')">关闭</button>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.modal-scrim {
  position: fixed;
  inset: 0;
  z-index: 500;
  background: oklch(0% 0 0 / 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}
.modal {
  width: min(640px, 96vw);
  max-height: 88vh;
  display: flex;
  flex-direction: column;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-lg, 12px);
  box-shadow: var(--shadow-modal);
}
.modal__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--color-border);
}
.modal__title {
  margin: 0;
  font-size: var(--text-section);
  font-weight: 700;
  color: var(--color-text);
}
.ai-spark {
  color: var(--color-primary);
}
.modal__close {
  width: 28px;
  height: 28px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
}
.modal__close:hover {
  color: var(--color-text);
  border-color: var(--color-text);
}
.modal__body {
  padding: 18px 20px;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.center {
  display: flex;
  align-items: center;
  gap: 10px;
  justify-content: center;
  padding: 36px 0;
  color: var(--color-dim);
  font-size: var(--text-label);
}
.center.err {
  color: var(--color-red);
}
.spinner {
  width: 16px;
  height: 16px;
  border: 2px solid var(--color-border-strong);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}
@keyframes spin {
  to { transform: rotate(360deg); }
}
.unavail {
  padding: 24px 4px;
}
.unavail__t {
  margin: 0 0 6px;
  font-weight: 700;
  color: var(--color-amber);
}
.unavail__r {
  margin: 0;
  color: var(--color-dim);
  font-size: var(--text-label);
}

.block {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.block__h {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: var(--text-caps);
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-faint);
  font-weight: 700;
}
.conf {
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 1px 8px;
  border-radius: 999px;
  text-transform: none;
  letter-spacing: 0;
}
.conf--high { color: var(--color-green); background: var(--color-green-soft); }
.conf--medium { color: var(--color-amber); background: var(--color-amber-soft); }
.conf--low { color: var(--color-faint); background: var(--color-inset); }
.hyp {
  margin: 0;
  font-size: var(--text-body);
  line-height: 1.6;
  color: var(--color-text);
}
.fixlist {
  margin: 0;
  padding-left: 20px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: var(--text-label);
  color: var(--color-text);
  line-height: 1.55;
}
.fixlist--dim {
  color: var(--color-dim);
}
.copy {
  font-size: var(--text-micro);
  padding: 2px 9px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
  text-transform: none;
  letter-spacing: 0;
}
.copy:hover {
  color: var(--color-text);
  border-color: var(--color-text);
}
.script {
  margin: 0;
  padding: 12px 14px;
  background: var(--color-term);
  color: oklch(88% 0.06 150);
  border-radius: var(--rounded-sm);
  font-size: var(--text-mono);
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
.evidence {
  margin: 0;
  padding: 10px 12px;
  background: var(--color-term);
  border-radius: var(--rounded-sm);
  font-size: var(--text-mono);
  line-height: 1.5;
  color: oklch(72% 0.01 250);
  max-height: 220px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
}
.evline {
  display: block;
}
.evline--hl {
  color: var(--color-amber);
  background: var(--color-amber-soft);
}
.modal__foot {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 20px;
  border-top: 1px solid var(--color-border);
}
.foot-hint {
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.grow {
  flex: 1;
}
.btn {
  font-size: var(--text-label);
  font-weight: 600;
  padding: 8px 16px;
  border-radius: var(--rounded-sm);
  border: 1px solid transparent;
  cursor: pointer;
}
.btn--ghost {
  background: transparent;
  border-color: var(--color-border-strong);
  color: var(--color-dim);
}
.btn--ghost:hover {
  color: var(--color-text);
  border-color: var(--color-text);
}
.btn--primary {
  background: var(--color-primary);
  color: #fff;
}
.btn--primary:hover {
  background: var(--color-primary-press);
}
</style>
