<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { login } from '../api/auth'
import { HttpError } from '../api/http'
import { listProjects } from '../api/projects'
import { isOnboardingDismissed } from '../composables/useOnboarding'
import { useSessionStore } from '../stores/session'
import ThemeToggle from '../components/ThemeToggle.vue'

const router = useRouter()
const route = useRoute()
const sessionStore = useSessionStore()

const username = ref('')
const password = ref('')
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
    usernameError.value = '请输入用户名'
    ok = false
  }
  if (!password.value) {
    passwordError.value = '请输入密码'
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
        bannerError.value =
          err.apiError?.message ?? '登录失败次数过多,请稍后重试'
      } else if (err.status === 401) {
        bannerError.value =
          err.apiError?.message ?? '用户名或口令错误'
        passwordError.value = '请检查用户名与密码'
      } else if (err.status === 0) {
        // Network error — backend may not be up
        bannerError.value = '无法连接到服务器,请检查后端是否运行后重试'
      } else {
        bannerError.value = err.apiError?.message ?? `登录失败(${err.status})`
      }
    } else {
      bannerError.value = '未知错误,请重试'
    }
  } finally {
    loading.value = false
  }
}

const canSubmit = computed(() => !loading.value && !isLockedOut.value)
</script>

<template>
  <div class="login-page">
    <div class="login-card" role="main">
      <!-- Brand header -->
      <div class="brand">
        <div class="brand-logo" aria-hidden="true">
          <span class="mono">d&gt;</span>
        </div>
        <div class="brand-name">
          <span class="brand-title">devopsTool</span>
          <span class="brand-sub">自托管 · 单管理员</span>
        </div>
      </div>

      <!-- Login form -->
      <form
        class="login-form"
        novalidate
        aria-label="登录表单"
        @submit.prevent="handleSubmit"
      >
        <!-- Banner error (401 / 429 / network) -->
        <div
          v-if="bannerError"
          class="banner"
          :class="{ 'banner--locked': isLockedOut }"
          role="alert"
          aria-live="assertive"
        >
          <span class="banner-icon" aria-hidden="true">{{ isLockedOut ? '⊘' : '⚠' }}</span>
          <span>{{ bannerError }}</span>
        </div>

        <!-- Username field -->
        <div class="field">
          <label class="field-label" for="username">账号</label>
          <input
            id="username"
            v-model="username"
            class="field-input"
            :class="{ 'field-input--error': usernameError }"
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
          <span
            v-if="usernameError"
            id="username-err"
            class="field-error"
            role="alert"
          >{{ usernameError }}</span>
        </div>

        <!-- Password field -->
        <div class="field">
          <label class="field-label" for="password">密码</label>
          <input
            id="password"
            v-model="password"
            class="field-input"
            :class="{ 'field-input--error': passwordError }"
            type="password"
            autocomplete="current-password"
            placeholder="••••••••"
            :disabled="loading || isLockedOut"
            :aria-invalid="passwordError ? 'true' : undefined"
            :aria-describedby="passwordError ? 'password-err' : undefined"
            @input="passwordError = ''"
          />
          <span
            v-if="passwordError"
            id="password-err"
            class="field-error"
            role="alert"
          >{{ passwordError }}</span>
        </div>

        <!-- Submit button -->
        <button
          type="submit"
          class="submit-btn"
          :disabled="!canSubmit"
          :aria-busy="loading"
        >
          <span v-if="loading" class="spinner" aria-hidden="true" />
          <span>{{ loading ? '登录中…' : '登录' }}</span>
        </button>
      </form>

      <!-- Instance info footer -->
      <div class="login-footer">
        <span class="status-dot" aria-hidden="true" />
        <span>Go 单二进制 · v0.1</span>
      </div>
    </div>

    <!-- Theme toggle -->
    <ThemeToggle />
  </div>
</template>

<style scoped>
.login-page {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 24px;
  /* Dual radial glows: brand-blue top-left + subtle bottom-right for depth */
  background-image:
    radial-gradient(700px 480px at 28% 0%, oklch(66% 0.155 258 / 0.12), transparent 58%),
    radial-gradient(600px 400px at 92% 100%, var(--color-inset), transparent 50%);
}

/* ——— Card ——— */
.login-card {
  width: 380px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow-modal);
  padding: 34px 32px;
  animation: card-in 0.6s var(--ease-out-expo) both;
}

@keyframes card-in {
  from {
    opacity: 0;
    transform: translateY(16px) scale(0.98);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

/* ——— Brand ——— */
.brand {
  display: flex;
  align-items: center;
  gap: 11px;
  margin-bottom: 26px;
}

.brand-logo {
  width: 38px;
  height: 38px;
  flex-shrink: 0;
  border-radius: var(--rounded-lg);
  background: var(--color-primary);
  color: #fff;
  display: grid;
  place-items: center;
  font-size: 1.05rem;
  box-shadow: 0 6px 20px var(--color-primary-soft);
}

.brand-name {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.brand-title {
  font-size: 1.12rem;
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--color-text);
}

.brand-sub {
  font-size: 0.72rem;
  color: var(--color-faint);
  font-weight: 400;
}

/* ——— Banner ——— */
.banner {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 14px;
  border-radius: var(--rounded);
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  color: var(--color-red);
  font-size: 0.83rem;
  line-height: 1.5;
  margin-bottom: 16px;
}

.banner--locked {
  background: var(--color-amber-soft);
  border-color: var(--color-amber-line);
  color: var(--color-amber);
}

.banner-icon {
  flex-shrink: 0;
  margin-top: 1px;
}

/* ——— Form fields ——— */
.login-form {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 16px;
}

.field-label {
  font-size: 0.78rem;
  font-weight: 500;
  color: var(--color-dim);
}

.field-input {
  width: 100%;
  height: 42px;
  background: var(--color-inset);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  padding: 0 13px;
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 0.9rem;
  transition:
    border-color var(--duration-fast),
    box-shadow var(--duration-fast);
}

.field-input::placeholder {
  color: var(--color-faint);
}

.field-input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

.field-input--error {
  border-color: var(--color-red);
}

.field-input--error:focus {
  border-color: var(--color-red);
  box-shadow: 0 0 0 3px var(--color-red-soft);
}

.field-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.field-error {
  font-size: 0.76rem;
  color: var(--color-red);
  line-height: 1.4;
}

/* ——— Submit button ——— */
.submit-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  width: 100%;
  height: 44px;
  margin-top: 8px;
  border: none;
  background: var(--color-primary);
  color: #fff;
  font-family: var(--font-sans);
  font-weight: 600;
  font-size: 0.92rem;
  border-radius: var(--rounded-lg);
  cursor: pointer;
  box-shadow: 0 8px 22px var(--color-primary-soft);
  transition:
    background-color var(--duration-fast),
    transform var(--duration-fast),
    box-shadow var(--duration-fast);
}

.submit-btn:hover:not(:disabled) {
  background: var(--color-primary-press);
  transform: translateY(-1px);
}

.submit-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 3px;
}

.submit-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
  box-shadow: none;
}

/* Spinner */
.spinner {
  display: inline-block;
  width: 15px;
  height: 15px;
  border: 2px solid rgba(255, 255, 255, 0.35);
  border-top-color: #fff;
  border-radius: var(--rounded-full);
  animation: spin 0.7s linear infinite;
  flex-shrink: 0;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* ——— Footer ——— */
.login-footer {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  margin-top: 20px;
  font-size: 0.74rem;
  color: var(--color-faint);
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  background: var(--color-green);
  flex-shrink: 0;
}
</style>
