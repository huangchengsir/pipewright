<script setup lang="ts">
/**
 * OnboardingFlow — first-run guide (UX-DR11).
 *
 * Value props (3 cards) + a 3-step checklist:
 *   1. 连接 AI 提供商   — depends on 7-1 (not built) → "即将可用", links to /settings/ai
 *   2. 添加第一台服务器 — depends on 4-1 (not built) → "即将可用", links to /servers
 *   3. 创建第一个项目   — REAL CTA (→ /projects); the only step truly judged complete
 *
 * Steps lock by dependency in the visual sense: AI/Server show "即将可用" and never
 * block; the project step is the live action. Progress counts only真实可判定 steps.
 *
 * Skip writes localStorage(onboarding_dismissed); re-openable from 设置.
 */
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import AppButton from '../ui/AppButton.vue'
import type { OnboardingStatus } from '../../composables/useOnboarding'

const props = defineProps<{
  status: OnboardingStatus
}>()

const emit = defineEmits<{
  skip: []
}>()

const router = useRouter()

type StepState = 'done' | 'now' | 'soon'

interface StepView {
  key: string
  num: string
  title: string
  badge?: string
  desc: string
  state: StepState
  /** action kind: 'go' real CTA, 'soon' coming-soon link, 'done' completed marker */
  ctaLabel?: string
  to?: string
  doneLabel?: string
}

const steps = computed<StepView[]>(() => {
  // The project step is the only真实 gate; AI/Server are forward-declared.
  return [
    {
      key: 'ai',
      num: '1',
      title: '连接 AI 提供商',
      badge: '诊断王牌',
      desc: '接入你自己的 LLM(Claude / OpenAI / 本地 Ollama),密钥存入加密保险库。',
      state: 'soon',
      ctaLabel: '前往配置 →',
      to: '/settings/ai',
    },
    {
      key: 'server',
      num: '2',
      title: '添加第一台服务器',
      desc: '填 host + 选 SSH 凭据,平台校验 SSH + Docker 连通性。agentless,无需在目标机装任何东西。',
      state: 'soon',
      ctaLabel: '前往配置 →',
      to: '/servers',
    },
    {
      key: 'project',
      num: '3',
      title: '创建第一个项目',
      desc: '接入 Gitee 仓库,AI 分析代码自动生成流水线配置。',
      state: props.status.hasProject ? 'done' : 'now',
      ctaLabel: '创建项目 →',
      to: '/projects',
      doneLabel: '已创建项目',
    },
  ]
})

// Progress is judged only on真实可判定 steps (本期仅「建项目」)。
const doneCount = computed(() => (props.status.hasProject ? 1 : 0))
const realStepCount = 1
const progressPct = computed(() => `${(doneCount.value / realStepCount) * 100}%`)

function goto(to?: string): void {
  if (to) {
    router.push(to)
  }
}

function skip(): void {
  emit('skip')
  router.push('/')
}
</script>

<template>
  <div class="onboarding">
    <!-- Hero -->
    <header class="ob-hero">
      <div class="ob-mark mono" aria-hidden="true">d&gt;</div>
      <div>
        <h1 class="ob-title">欢迎使用 Pipewright</h1>
        <p class="ob-lede">
          把 <b>CI + 部署编排 + 服务器管理</b> 收进一个 <b>≤100MB</b> 的二进制,并把 AI 做进失败诊断。三步即可让第一个服务跑起来。
        </p>
      </div>
    </header>

    <!-- Value props -->
    <section class="ob-props" aria-label="平台价值">
      <article class="ob-prop ob-prop--green">
        <div class="ob-prop__i" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9"><path d="M13 2 3 14h7l-1 8 10-12h-7z" /></svg>
        </div>
        <b class="ob-prop__h">轻量 <span class="ob-prop__u">≤100MB</span></b>
        <span class="ob-prop__d">单二进制,Docker 或原生直跑,远低于 Jenkins 500MB+。</span>
      </article>
      <article class="ob-prop">
        <div class="ob-prop__i" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9"><rect x="3" y="3" width="7" height="7" rx="1.5" /><rect x="14" y="3" width="7" height="7" rx="1.5" /><rect x="3" y="14" width="7" height="7" rx="1.5" /><rect x="14" y="14" width="7" height="7" rx="1.5" /></svg>
        </div>
        <b class="ob-prop__h">一个工具替三件套</b>
        <span class="ob-prop__d">CI + Ansible/Kamal + Portainer,从 push 到多机部署一站搞定。</span>
      </article>
      <article class="ob-prop ob-prop--cyan">
        <div class="ob-prop__i" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12h3.5l2.2-6 3.6 12 2.4-7 1.3 1h4.5" /></svg>
        </div>
        <b class="ob-prop__h">AI 失败诊断</b>
        <span class="ob-prop__d">构建/部署失败时直接给根因假说 + 证据 + 修复建议。</span>
      </article>
    </section>

    <!-- Setup checklist -->
    <section class="ob-setup" aria-label="开始设置">
      <div class="ob-setup__h">
        <div class="ob-setup__t">
          开始设置
          <span>完成后即可触发第一次部署</span>
        </div>
        <div class="ob-setup__prog">
          <span class="ob-setup__n mono">{{ doneCount }} / {{ realStepCount }}</span>
          <div class="ob-setup__bar"><i :style="{ width: progressPct }" /></div>
        </div>
      </div>

      <div
        v-for="step in steps"
        :key="step.key"
        class="ob-step"
        :class="`ob-step--${step.state}`"
      >
        <span class="ob-step__num mono" aria-hidden="true">
          <svg v-if="step.state === 'done'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2"><path d="m5 13 4 4 10-11" /></svg>
          <template v-else>{{ step.num }}</template>
        </span>
        <div class="ob-step__bd">
          <b class="ob-step__title">
            {{ step.title }}
            <span v-if="step.badge" class="ob-step__badge">{{ step.badge }}</span>
            <span v-if="step.state === 'soon'" class="ob-step__soon">即将可用</span>
          </b>
          <span class="ob-step__desc">{{ step.desc }}</span>
        </div>
        <div class="ob-step__act">
          <span v-if="step.state === 'done'" class="ob-step__done">
            <span class="ob-step__done-dot" aria-hidden="true" />{{ step.doneLabel }}
          </span>
          <AppButton
            v-else-if="step.state === 'now'"
            variant="primary"
            @click="goto(step.to)"
          >
            {{ step.ctaLabel }}
          </AppButton>
          <AppButton
            v-else
            variant="default"
            @click="goto(step.to)"
          >
            {{ step.ctaLabel }}
          </AppButton>
        </div>
      </div>
    </section>

    <p class="ob-skip">
      也可以
      <button type="button" class="ob-skip__link" @click="skip">跳过引导,直接进入控制台</button>
      · 引导项随时可在设置中重新打开
    </p>
  </div>
</template>

<style scoped>
.onboarding {
  max-width: 1080px;
  margin: 0 auto;
  padding: 8px 0 60px;
}

/* ——— hero ——— */
.ob-hero {
  display: flex;
  align-items: flex-start;
  gap: 18px;
  margin-bottom: 30px;
  animation: ob-in 0.5s var(--ease-out-expo) both;
}
.ob-mark {
  width: 54px;
  height: 54px;
  border-radius: var(--rounded-card);
  background: var(--color-primary);
  color: #fff;
  display: grid;
  place-items: center;
  font-weight: 700;
  font-size: 1.5rem;
  flex: none;
  box-shadow: 0 8px 26px var(--color-primary-soft);
}
.ob-title {
  font-size: var(--text-display);
  font-weight: 700;
  letter-spacing: -0.025em;
  color: var(--color-text);
}
.ob-lede {
  font-size: 0.95rem;
  color: var(--color-dim);
  margin-top: 6px;
  max-width: 62ch;
  line-height: 1.55;
}
.ob-lede b {
  color: var(--color-text);
  font-weight: 600;
}

/* ——— value props ——— */
.ob-props {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 14px;
  margin-bottom: 30px;
}
.ob-prop {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-xl);
  box-shadow: var(--shadow);
  padding: 16px 17px;
  animation: ob-in 0.5s 0.05s var(--ease-out-expo) both;
}
.ob-prop__i {
  width: 32px;
  height: 32px;
  border-radius: var(--rounded);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  display: grid;
  place-items: center;
  margin-bottom: 11px;
}
.ob-prop__i svg {
  width: 17px;
  height: 17px;
}
.ob-prop--cyan .ob-prop__i {
  background: var(--color-cyan-soft);
  color: var(--color-cyan);
}
.ob-prop--green .ob-prop__i {
  background: var(--color-green-soft);
  color: var(--color-green);
}
.ob-prop__h {
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--color-text);
}
.ob-prop__u {
  font-size: 0.72rem;
  color: var(--color-faint);
  font-weight: 500;
}
.ob-prop__d {
  display: block;
  font-size: 0.79rem;
  color: var(--color-faint);
  margin-top: 5px;
  line-height: 1.5;
}

/* ——— setup checklist ——— */
.ob-setup {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
  animation: ob-in 0.55s 0.1s var(--ease-out-expo) both;
}
.ob-setup__h {
  display: flex;
  align-items: center;
  gap: 13px;
  padding: 17px 20px;
  border-bottom: 1px solid var(--color-border);
}
.ob-setup__t {
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-text);
}
.ob-setup__t span {
  display: block;
  font-size: 0.78rem;
  color: var(--color-faint);
  font-weight: 400;
  margin-top: 2px;
}
.ob-setup__prog {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 10px;
}
.ob-setup__n {
  font-size: 0.78rem;
  color: var(--color-dim);
}
.ob-setup__bar {
  width: 120px;
  height: 6px;
  border-radius: var(--rounded-full);
  background: var(--color-inset);
  overflow: hidden;
}
.ob-setup__bar i {
  display: block;
  height: 100%;
  background: var(--color-primary);
  border-radius: var(--rounded-full);
  transition: width var(--duration-normal, 300ms) var(--ease-out-expo);
}

.ob-step {
  display: flex;
  align-items: center;
  gap: 15px;
  padding: 16px 20px;
  border-bottom: 1px solid var(--color-border);
}
.ob-step:last-child {
  border-bottom: none;
}
.ob-step__num {
  width: 30px;
  height: 30px;
  border-radius: var(--rounded);
  display: grid;
  place-items: center;
  flex: none;
  font-weight: 600;
  font-size: 0.85rem;
}
.ob-step__num svg {
  width: 15px;
  height: 15px;
}
.ob-step--done .ob-step__num {
  background: var(--color-green-soft);
  color: var(--color-green);
}
.ob-step--now .ob-step__num {
  background: var(--color-primary);
  color: #fff;
  box-shadow: 0 4px 14px var(--color-primary-soft);
}
.ob-step--soon .ob-step__num {
  background: var(--color-inset);
  color: var(--color-faint);
  border: 1px dashed var(--color-border-strong);
}
.ob-step__bd {
  flex: 1;
  min-width: 0;
}
.ob-step__title {
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--color-text);
  display: inline-flex;
  align-items: center;
  gap: 7px;
  flex-wrap: wrap;
}
.ob-step--soon .ob-step__title {
  color: var(--color-faint);
}
.ob-step__badge {
  font-size: 0.66rem;
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border-radius: var(--rounded-sm);
  padding: 1px 7px;
  font-weight: 500;
}
.ob-step__soon {
  font-size: 0.66rem;
  color: var(--color-faint);
  background: var(--color-inset);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-sm);
  padding: 1px 7px;
  font-weight: 500;
}
.ob-step__desc {
  display: block;
  font-size: 0.79rem;
  color: var(--color-faint);
  margin-top: 3px;
  line-height: 1.5;
}
.ob-step__act {
  flex: none;
}
.ob-step__done {
  font-size: 0.78rem;
  color: var(--color-green);
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.ob-step__done-dot {
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  background: var(--color-green);
}

.ob-skip {
  text-align: center;
  margin-top: 22px;
  font-size: 0.82rem;
  color: var(--color-faint);
}
.ob-skip__link {
  color: var(--color-dim);
  cursor: pointer;
  text-decoration: underline;
  text-underline-offset: 3px;
  background: none;
  border: none;
  font: inherit;
  padding: 0;
}
.ob-skip__link:hover {
  color: var(--color-text);
}
.ob-skip__link:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
  border-radius: var(--rounded-sm);
}

@keyframes ob-in {
  from {
    opacity: 0;
    transform: translateY(14px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}
@media (prefers-reduced-motion: reduce) {
  .ob-hero,
  .ob-prop,
  .ob-setup {
    animation: none;
  }
  .ob-setup__bar i {
    transition: none;
  }
}

@media (max-width: 720px) {
  .ob-props {
    grid-template-columns: 1fr;
  }
  .ob-step {
    flex-wrap: wrap;
  }
}
</style>
