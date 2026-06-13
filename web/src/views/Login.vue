<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter, useRoute } from 'vue-router'
import { login } from '../api/auth'
import { HttpError } from '../api/http'
import { listProjects } from '../api/projects'
import { isOnboardingDismissed } from '../composables/useOnboarding'
import { useSessionStore } from '../stores/session'

const router = useRouter()
const route = useRoute()
const sessionStore = useSessionStore()
const { t } = useI18n()

const username = ref('')
const password = ref('')
const showPassword = ref(false)
const loading = ref(false)

// Field-level errors
const usernameError = ref('')
const passwordError = ref('')

// Banner-level error (shown for 401 / 429 / network)
const bannerError = ref('')

// Whether this is a lockout (429)
const isLockedOut = ref(false)

function clearErrors(): void {
  usernameError.value = ''
  passwordError.value = ''
  bannerError.value = ''
  isLockedOut.value = false
}

function validate(): boolean {
  clearErrors()
  let ok = true
  if (!username.value.trim()) {
    usernameError.value = t('login.errUsername')
    ok = false
  }
  if (!password.value) {
    passwordError.value = t('login.errPassword')
    ok = false
  }
  return ok
}

async function handleSubmit(): Promise<void> {
  if (!validate()) return
  loading.value = true
  clearErrors()

  try {
    const user = await login(username.value.trim(), password.value)
    // Prime the session cache so the route guard sees the fresh login
    // instead of the stale "unauthenticated" result from app startup.
    sessionStore.setUser(user)
    // Success — navigate to redirect target or root.
    // Validate redirect: must start with '/' and must NOT start with '//'
    // (double-slash opens a protocol-relative open-redirect vector).
    const rawRedirect = route.query.redirect as string | undefined
    const hasExplicitRedirect =
      typeof rawRedirect === 'string' &&
      rawRedirect.startsWith('/') &&
      !rawRedirect.startsWith('//')

    if (hasExplicitRedirect) {
      await router.replace(rawRedirect as string)
      return
    }

    // No explicit target: first-run instances (no project, not dismissed) land on
    // the onboarding guide; otherwise fall to the overview. hasAI/hasServer are
    // forward-declared (7-1/4-1 not built) so onboarding is gated on project存在.
    let goOnboarding = false
    if (!isOnboardingDismissed()) {
      try {
        const projects = await listProjects()
        goOnboarding = projects.length === 0
      } catch {
        // Projects unreachable — don't block login; fall to overview.
        goOnboarding = false
      }
    }
    await router.replace(goOnboarding ? '/onboarding' : '/')
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 429) {
        isLockedOut.value = true
        bannerError.value = err.apiError?.message ?? t('login.errLockout')
      } else if (err.status === 401) {
        bannerError.value = err.apiError?.message ?? t('login.errCredentials')
        passwordError.value = t('login.errCheckCredentials')
      } else if (err.status === 0) {
        // Network error — backend may not be up
        bannerError.value = t('login.errNetwork')
      } else {
        bannerError.value = err.apiError?.message ?? t('login.errGeneric', { status: err.status })
      }
    } else {
      bannerError.value = t('login.errUnknown')
    }
  } finally {
    loading.value = false
  }
}

const canSubmit = computed(() => !loading.value && !isLockedOut.value)

// ─── 沉浸式氛围:浮动粒子 + 指针视差(纯装饰,尊重 prefers-reduced-motion) ───────
// 全部自包含、无外部资源(self-hosted「数据不出网」铁律):字体用项目 token,粒子用 canvas。
const particlesCanvas = ref<HTMLCanvasElement | null>(null)
const cardShell = ref<HTMLElement | null>(null)
let rafId = 0
let onResize: (() => void) | null = null
let onPointerMove: ((e: PointerEvent) => void) | null = null

interface Particle {
  x: number
  y: number
  r: number
  vx: number
  vy: number
  a: number
  hue: number
}

onMounted(() => {
  const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  if (reduce) return

  // 指针视差:卡片随鼠标轻微 3D 倾斜。
  const shell = cardShell.value
  if (shell) {
    onPointerMove = (e: PointerEvent) => {
      const dx = e.clientX / window.innerWidth - 0.5
      const dy = e.clientY / window.innerHeight - 0.5
      shell.style.transform = `perspective(1200px) rotateY(${dx * 5}deg) rotateX(${-dy * 5}deg)`
    }
    window.addEventListener('pointermove', onPointerMove)
  }

  // 浮动粒子:上升的彩色光点,品牌色 hue。
  const cv = particlesCanvas.value
  const ctx = cv?.getContext('2d')
  if (!cv || !ctx) return

  let w = 0
  let h = 0
  let parts: Particle[] = []
  // 品牌蓝族 hue(HSL):电光蓝 / 青 / 靛 / 天蓝 —— 去掉原先的紫(265)和粉(320)。
  const hues = [217, 190, 235, 205]

  const makeParticle = (): Particle => ({
    x: Math.random() * w,
    y: Math.random() * h,
    r: Math.random() * 1.8 + 0.4,
    vx: (Math.random() - 0.5) * 0.18,
    vy: -(Math.random() * 0.35 + 0.08),
    a: Math.random() * 0.5 + 0.15,
    hue: hues[Math.floor(Math.random() * hues.length)],
  })

  onResize = () => {
    w = cv.width = window.innerWidth
    h = cv.height = window.innerHeight
    const count = Math.min(90, Math.floor((w * h) / 18000))
    parts = Array.from({ length: count }, makeParticle)
  }
  onResize()
  window.addEventListener('resize', onResize)

  const tick = (): void => {
    ctx.clearRect(0, 0, w, h)
    for (const p of parts) {
      p.x += p.vx
      p.y += p.vy
      if (p.y < -10) {
        p.y = h + 10
        p.x = Math.random() * w
      }
      if (p.x < -10) p.x = w + 10
      if (p.x > w + 10) p.x = -10
      ctx.beginPath()
      ctx.arc(p.x, p.y, p.r, 0, 7)
      ctx.fillStyle = `hsla(${p.hue},90%,70%,${p.a})`
      ctx.shadowBlur = 8
      ctx.shadowColor = `hsla(${p.hue},90%,70%,${p.a})`
      ctx.fill()
    }
    ctx.shadowBlur = 0
    rafId = window.requestAnimationFrame(tick)
  }
  tick()
})

onBeforeUnmount(() => {
  if (rafId) window.cancelAnimationFrame(rafId)
  if (onResize) window.removeEventListener('resize', onResize)
  if (onPointerMove) window.removeEventListener('pointermove', onPointerMove)
})
</script>

<template>
  <div class="login-page">
    <!-- animated aurora background -->
    <div class="aurora" aria-hidden="true">
      <span class="blob b1" />
      <span class="blob b2" />
      <span class="blob b3" />
      <span class="blob b4" />
      <span class="blob b5" />
    </div>
    <canvas ref="particlesCanvas" class="particles" aria-hidden="true" />
    <div class="grain" aria-hidden="true" />

    <!-- top-left brand -->
    <div class="brand">
      <div class="brand-logo mono" aria-hidden="true">p&gt;</div>
      <div class="brand-name">pipe<span class="brand-name__w">wright</span></div>
    </div>

    <!-- glass card -->
    <div class="wrap">
      <div ref="cardShell" class="card-shell">
        <div class="card" role="main">
          <div class="card__pool" aria-hidden="true" />
          <div class="card__sheen" aria-hidden="true" />

          <span class="eyebrow"><span class="eyebrow__dot" aria-hidden="true" />{{ t('login.eyebrow') }}</span>
          <h1 class="headline">
            {{ t('login.headlineWelcome') }}<br />{{ t('login.headlineEnter') }}
            <span class="headline__grad">{{ t('login.headlineWord') }}</span>{{ t('login.headlinePunct') }}
          </h1>
          <p class="lede">{{ t('login.lede') }}</p>

          <form class="login-form" novalidate :aria-label="t('login.formAria')" @submit.prevent="handleSubmit">
            <!-- Banner error (401 / 429 / network) -->
            <div
              v-if="bannerError"
              class="banner"
              :class="{ 'banner--locked': isLockedOut }"
              role="alert"
              aria-live="assertive"
            >
              <span class="banner__icon" aria-hidden="true">{{ isLockedOut ? '⊘' : '⚠' }}</span>
              <span>{{ bannerError }}</span>
            </div>

            <!-- Username field -->
            <div class="field">
              <label class="field__label" for="username">{{ t('login.account') }}</label>
              <div class="inwrap" :class="{ 'inwrap--error': usernameError }">
                <span class="inwrap__ic" aria-hidden="true">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
                    <circle cx="12" cy="8" r="4" />
                    <path d="M4 21c0-4 4-6 8-6s8 2 8 6" />
                  </svg>
                </span>
                <input
                  id="username"
                  v-model="username"
                  class="field__input"
                  type="text"
                  autocomplete="username"
                  autocorrect="off"
                  autocapitalize="off"
                  spellcheck="false"
                  placeholder="admin"
                  :disabled="loading || isLockedOut"
                  :aria-invalid="usernameError ? 'true' : undefined"
                  :aria-describedby="usernameError ? 'username-err' : undefined"
                  @input="usernameError = ''"
                />
              </div>
              <span v-if="usernameError" id="username-err" class="field__error" role="alert">{{
                usernameError
              }}</span>
            </div>

            <!-- Password field -->
            <div class="field">
              <label class="field__label" for="password">{{ t('login.password') }}</label>
              <div class="inwrap" :class="{ 'inwrap--error': passwordError }">
                <span class="inwrap__ic" aria-hidden="true">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
                    <rect x="4" y="11" width="16" height="9" rx="2" />
                    <path d="M8 11V7a4 4 0 0 1 8 0v4" />
                  </svg>
                </span>
                <input
                  id="password"
                  v-model="password"
                  class="field__input"
                  :type="showPassword ? 'text' : 'password'"
                  autocomplete="current-password"
                  placeholder="••••••••••••"
                  :disabled="loading || isLockedOut"
                  :aria-invalid="passwordError ? 'true' : undefined"
                  :aria-describedby="passwordError ? 'password-err' : undefined"
                  @input="passwordError = ''"
                />
                <button
                  type="button"
                  class="reveal"
                  :aria-label="showPassword ? t('login.hidePassword') : t('login.showPassword')"
                  @click="showPassword = !showPassword"
                >
                  <svg
                    v-if="!showPassword"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="1.8"
                  >
                    <path d="M2 12s4-7 10-7 10 7 10 7-4 7-10 7-10-7-10-7z" />
                    <circle cx="12" cy="12" r="3" />
                  </svg>
                  <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
                    <path d="M2 12s4-7 10-7a9.7 9.7 0 0 1 5 1.3M22 12s-4 7-10 7a9.7 9.7 0 0 1-5-1.3" />
                    <path d="M4 4l16 16" />
                  </svg>
                </button>
              </div>
              <span v-if="passwordError" id="password-err" class="field__error" role="alert">{{
                passwordError
              }}</span>
            </div>

            <button
              type="submit"
              class="submit"
              :disabled="!canSubmit"
              :aria-busy="loading"
            >
              <span class="submit__shine" aria-hidden="true" />
              <span v-if="loading" class="spinner" aria-hidden="true" />
              <span>{{ loading ? t('login.connecting') : t('login.enter') }}</span>
              <svg
                v-if="!loading"
                class="submit__arrow"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2.4"
                stroke-linecap="round"
                stroke-linejoin="round"
              >
                <path d="M5 12h14M13 6l6 6-6 6" />
              </svg>
            </button>
          </form>
        </div>
      </div>
    </div>

    <!-- bottom trust bar -->
    <div class="trust" aria-hidden="true">
      <span>{{ t('login.trustSelfHosted') }}</span><span class="trust__s">·</span>
      <span>{{ t('login.trustNoEgress') }}</span><span class="trust__s">·</span>
      <span>{{ t('login.trustSingleBinary') }}</span>
    </div>
  </div>
</template>

<style scoped>
/* 沉浸式深色驾驶舱面:固定深色调,不随全局浅/深主题翻转(登录是 pre-auth 独立场景)。
   所有色值显式写死、字体走项目 token,无任何外部资源(self-hosted「数据不出网」)。 */
.login-page {
  /* 与 app 唯一品牌色「电光蓝」(--color-primary, hue 258)对齐:只用蓝 / 靛 / 青 / 天蓝
     同族色,不再用紫/粉彩虹。深色门厅过渡到浅色 app,色相保持一致。 */
  --lp-bg: #060a14;
  --lp-blue: #3b82f6; /* 品牌电光蓝(主) */
  --lp-deep: #2563eb; /* 深蓝(纵深) */
  --lp-indigo: #6366f1; /* 靛(临近) */
  --lp-cyan: #22d3ee; /* 青(点缀) */
  --lp-sky: #38bdf8; /* 天蓝(临近) */
  --lp-text: #eef4ff;
  --lp-dim: #aebbd6;
  --lp-faint: #74809c;
  --lp-glass: rgba(255, 255, 255, 0.045);
  --lp-brd: rgba(255, 255, 255, 0.12);

  position: fixed;
  inset: 0;
  overflow: hidden;
  background: var(--lp-bg);
  color: var(--lp-text);
  font-family: var(--font-sans);
  font-size: 14px;
  line-height: 1.5;
}

.mono {
  font-family: var(--font-mono, ui-monospace, monospace);
}

/* ——— aurora background ——— */
.aurora {
  position: absolute;
  inset: 0;
  z-index: 0;
  overflow: hidden;
  filter: saturate(1.25);
}
.blob {
  position: absolute;
  border-radius: 50%;
  filter: blur(70px);
  opacity: 0.85;
  mix-blend-mode: screen;
  will-change: transform;
}
.b1 {
  width: 560px;
  height: 560px;
  background: radial-gradient(circle, var(--lp-blue), transparent 65%);
  top: -140px;
  left: -100px;
  animation: float1 18s ease-in-out infinite alternate;
}
.b2 {
  width: 520px;
  height: 520px;
  background: radial-gradient(circle, var(--lp-deep), transparent 65%);
  bottom: -160px;
  right: -80px;
  animation: float2 22s ease-in-out infinite alternate;
}
.b3 {
  width: 420px;
  height: 420px;
  background: radial-gradient(circle, var(--lp-cyan), transparent 65%);
  top: 30%;
  right: 12%;
  animation: float3 16s ease-in-out infinite alternate;
}
.b4 {
  width: 460px;
  height: 460px;
  background: radial-gradient(circle, var(--lp-sky), transparent 65%);
  bottom: 6%;
  left: 8%;
  animation: float4 20s ease-in-out infinite alternate;
}
.b5 {
  width: 380px;
  height: 380px;
  background: radial-gradient(circle, var(--lp-indigo), transparent 65%);
  top: 8%;
  left: 42%;
  animation: float1 24s ease-in-out infinite alternate-reverse;
}
@keyframes float1 {
  0% {
    transform: translate(0, 0) scale(1);
  }
  100% {
    transform: translate(140px, 120px) scale(1.25);
  }
}
@keyframes float2 {
  0% {
    transform: translate(0, 0) scale(1.1);
  }
  100% {
    transform: translate(-160px, -90px) scale(0.85);
  }
}
@keyframes float3 {
  0% {
    transform: translate(0, 0) scale(0.9);
  }
  100% {
    transform: translate(-120px, 140px) scale(1.2);
  }
}
@keyframes float4 {
  0% {
    transform: translate(0, 0) scale(1);
  }
  100% {
    transform: translate(120px, -120px) scale(1.15);
  }
}
.aurora::after {
  content: '';
  position: absolute;
  inset: 0;
  background: radial-gradient(
    120% 120% at 50% 40%,
    transparent 30%,
    rgba(6, 10, 20, 0.55) 75%,
    rgba(6, 10, 20, 0.92) 100%
  );
}

.particles {
  position: absolute;
  inset: 0;
  z-index: 1;
  pointer-events: none;
}
.grain {
  position: absolute;
  inset: 0;
  z-index: 2;
  pointer-events: none;
  opacity: 0.05;
  mix-blend-mode: overlay;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='120' height='120'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.9' numOctaves='3'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)'/%3E%3C/svg%3E");
}

/* ——— brand ——— */
.brand {
  position: absolute;
  top: 30px;
  left: 34px;
  z-index: 6;
  display: flex;
  align-items: center;
  gap: 11px;
  animation: rise 0.8s 0.1s cubic-bezier(0.2, 0.75, 0.2, 1) both;
}
.brand-logo {
  width: 38px;
  height: 38px;
  border-radius: 11px;
  display: grid;
  place-items: center;
  color: #fff;
  font-weight: 700;
  font-size: 15px;
  background: linear-gradient(135deg, var(--lp-blue), var(--lp-cyan));
  box-shadow: 0 6px 22px rgba(59, 130, 246, 0.5);
}
.brand-name {
  font-weight: 700;
  font-size: 17px;
  letter-spacing: -0.01em;
}
.brand-name__w {
  background: linear-gradient(90deg, var(--lp-blue), var(--lp-cyan));
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}

/* ——— layout ——— */
.wrap {
  position: relative;
  z-index: 5;
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px;
}

/* glass card with animated glow border */
.card-shell {
  position: relative;
  width: 100%;
  max-width: 440px;
  animation: cardIn 1.05s cubic-bezier(0.2, 0.75, 0.2, 1) both;
  transition: transform 0.1s ease-out;
}
.card-shell::before {
  content: '';
  position: absolute;
  inset: -1.5px;
  border-radius: 27px;
  z-index: 0;
  background: conic-gradient(
    from 0deg,
    var(--lp-blue),
    var(--lp-cyan),
    var(--lp-indigo),
    var(--lp-sky),
    var(--lp-blue)
  );
  filter: blur(9px);
  opacity: 0.55;
  animation: spin 9s linear infinite;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
.card {
  position: relative;
  z-index: 1;
  border-radius: 26px;
  padding: 40px 38px 34px;
  background: linear-gradient(160deg, rgba(255, 255, 255, 0.08), rgba(255, 255, 255, 0.03));
  backdrop-filter: blur(34px) saturate(1.3);
  border: 1px solid var(--lp-brd);
  box-shadow:
    0 30px 80px -20px rgba(0, 0, 0, 0.7),
    inset 0 1px 0 rgba(255, 255, 255, 0.18);
  overflow: hidden;
}
.card::after {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 120px;
  pointer-events: none;
  z-index: 3;
  background: radial-gradient(80% 120% at 30% -20%, rgba(255, 255, 255, 0.16), transparent 70%);
}
.card__sheen {
  position: absolute;
  top: -60%;
  left: 0;
  width: 38%;
  height: 220%;
  z-index: 2;
  pointer-events: none;
  background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.09), transparent);
  transform: translateX(-220%) rotate(16deg);
  animation: sweep 8.5s ease-in-out infinite;
}
@keyframes sweep {
  0%,
  68% {
    transform: translateX(-220%) rotate(16deg);
  }
  100% {
    transform: translateX(520%) rotate(16deg);
  }
}
.card__pool {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 130px;
  z-index: 0;
  pointer-events: none;
  background: radial-gradient(
    70% 100% at 50% 120%,
    rgba(59, 130, 246, 0.22),
    rgba(34, 211, 238, 0.12) 50%,
    transparent 75%
  );
}

.eyebrow {
  position: relative;
  z-index: 4;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--lp-dim);
  padding: 5px 11px;
  border-radius: 999px;
  border: 1px solid var(--lp-brd);
  background: rgba(255, 255, 255, 0.04);
  margin-bottom: 20px;
}
.eyebrow__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #34d399;
  box-shadow: 0 0 10px 1px rgba(52, 211, 153, 0.8);
  animation: pulse 1.6s ease-in-out infinite;
}
@keyframes pulse {
  50% {
    opacity: 0.4;
  }
}
.headline {
  position: relative;
  z-index: 4;
  font-weight: 700;
  font-size: 30px;
  letter-spacing: -0.025em;
  line-height: 1.14;
}
.headline__grad {
  background: linear-gradient(
    100deg,
    var(--lp-blue),
    var(--lp-cyan),
    var(--lp-indigo),
    var(--lp-blue)
  );
  background-size: 240% 100%;
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  animation: shimmer 6s ease infinite;
}
@keyframes shimmer {
  0%,
  100% {
    background-position: 0% 50%;
  }
  50% {
    background-position: 120% 50%;
  }
}
.lede {
  position: relative;
  z-index: 4;
  color: var(--lp-dim);
  font-size: 14.5px;
  margin-top: 10px;
}

.login-form {
  position: relative;
  z-index: 4;
  margin-top: 26px;
}

/* ——— banner ——— */
.banner {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 11px 14px;
  border-radius: 13px;
  background: rgba(244, 63, 94, 0.13);
  border: 1px solid rgba(244, 63, 94, 0.4);
  color: #fda4af;
  font-size: 13px;
  line-height: 1.5;
  margin-bottom: 18px;
}
.banner--locked {
  background: rgba(245, 158, 11, 0.13);
  border-color: rgba(245, 158, 11, 0.4);
  color: #fcd34d;
}
.banner__icon {
  flex-shrink: 0;
  margin-top: 1px;
}

/* ——— fields ——— */
.field {
  margin-bottom: 15px;
}
.field__label {
  display: block;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--lp-dim);
  margin-bottom: 8px;
}
.inwrap {
  position: relative;
  display: flex;
  align-items: center;
}
.inwrap__ic {
  position: absolute;
  left: 15px;
  color: var(--lp-faint);
  display: flex;
  pointer-events: none;
  transition: color 0.2s;
}
.inwrap__ic svg {
  width: 18px;
  height: 18px;
}
.field__input {
  width: 100%;
  padding: 14px 15px 14px 45px;
  border-radius: 14px;
  background: rgba(10, 9, 20, 0.5);
  border: 1px solid var(--lp-brd);
  color: var(--lp-text);
  font-family: var(--font-sans);
  font-size: 14.5px;
  outline: none;
  transition:
    border-color 0.2s,
    box-shadow 0.2s,
    background 0.2s;
}
.field__input::placeholder {
  color: var(--lp-faint);
}
.field__input:focus {
  border-color: transparent;
  box-shadow:
    0 0 0 1.5px var(--lp-blue),
    0 0 26px rgba(59, 130, 246, 0.35);
  background: rgba(10, 9, 20, 0.7);
}
.inwrap:focus-within .inwrap__ic {
  color: var(--lp-blue);
}
.inwrap--error .field__input {
  border-color: rgba(244, 63, 94, 0.6);
}
.field__input:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.reveal {
  position: absolute;
  right: 13px;
  background: none;
  border: none;
  color: var(--lp-faint);
  cursor: pointer;
  display: flex;
  padding: 5px;
  transition: color 0.2s;
}
.reveal:hover {
  color: var(--lp-cyan);
}
.reveal svg {
  width: 18px;
  height: 18px;
}
.field__error {
  display: block;
  font-size: 12px;
  color: #fda4af;
  margin-top: 7px;
}

/* ——— submit ——— */
.submit {
  position: relative;
  width: 100%;
  margin-top: 7px;
  padding: 16px;
  border-radius: 15px;
  border: none;
  cursor: pointer;
  overflow: hidden;
  font-family: var(--font-sans);
  font-size: 15px;
  font-weight: 700;
  color: #fff;
  letter-spacing: 0.01em;
  background: linear-gradient(100deg, var(--lp-blue), var(--lp-indigo), var(--lp-cyan));
  background-size: 200% 100%;
  box-shadow:
    0 12px 34px -8px rgba(59, 130, 246, 0.7),
    inset 0 1px 0 rgba(255, 255, 255, 0.3);
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  transition: transform 0.15s;
  animation: gradmove 5s ease infinite;
}
@keyframes gradmove {
  0%,
  100% {
    background-position: 0% 50%;
  }
  50% {
    background-position: 100% 50%;
  }
}
.submit:hover:not(:disabled) {
  transform: translateY(-2px);
  box-shadow:
    0 18px 44px -8px rgba(59, 130, 246, 0.85),
    inset 0 1px 0 rgba(255, 255, 255, 0.3);
}
.submit:active:not(:disabled) {
  transform: translateY(0);
}
.submit:disabled {
  opacity: 0.65;
  cursor: not-allowed;
  animation: none;
}
.submit__arrow {
  width: 17px;
  height: 17px;
}
.submit__shine {
  position: absolute;
  top: 0;
  left: -60%;
  width: 45%;
  height: 100%;
  background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.55), transparent);
  transform: skewX(-20deg);
}
.submit:hover:not(:disabled) .submit__shine {
  animation: shine 0.85s ease;
}
@keyframes shine {
  0% {
    left: -60%;
  }
  100% {
    left: 130%;
  }
}
.spinner {
  display: inline-block;
  width: 15px;
  height: 15px;
  border: 2px solid rgba(255, 255, 255, 0.35);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
  flex-shrink: 0;
}

/* ——— trust bar ——— */
.trust {
  position: absolute;
  bottom: 26px;
  left: 0;
  right: 0;
  z-index: 6;
  display: flex;
  justify-content: center;
  gap: 16px;
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  letter-spacing: 0.06em;
  color: var(--lp-faint);
  animation: rise 0.9s 0.9s ease both;
}
.trust__s {
  opacity: 0.5;
}

/* ——— entrance ——— */
@keyframes cardIn {
  from {
    opacity: 0;
    transform: translateY(30px) scale(0.975);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}
@keyframes rise {
  from {
    opacity: 0;
    transform: translateY(15px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .blob,
  .card-shell::before,
  .card__sheen,
  .headline__grad,
  .submit,
  .eyebrow__dot,
  .brand,
  .trust,
  .card-shell {
    animation: none;
  }
  .particles {
    display: none;
  }
}

@media (max-width: 520px) {
  .trust {
    display: none;
  }
  .card {
    padding: 32px 26px 28px;
  }
  .headline {
    font-size: 26px;
  }
}
</style>
