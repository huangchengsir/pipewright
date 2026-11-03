<script setup lang="ts">
import { shallowRef, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter, useRoute } from 'vue-router'
import {
  AlertCircle,
  ArrowRight,
  Eye,
  EyeOff,
  Lock,
  User,
} from '@vicons/tabler'
import { login } from '../../api/auth'
import { HttpError } from '../../api/http'
import { onboardingLoginTarget } from '../../composables/useOnboarding'
import { useSessionStore } from '../../stores/session'
import PlatformLoginEntry from './PlatformLoginEntry.vue'
const router = useRouter()
const route = useRoute()
const sessionStore = useSessionStore()
const { t } = useI18n()

const username = shallowRef('')
const password = shallowRef('')
const showPassword = shallowRef(false)
const loading = shallowRef(false)

// Field-level errors
const usernameError = shallowRef('')
const passwordError = shallowRef('')

// Banner-level error (shown for 401 / 429 / network)
const bannerError = shallowRef('')

// Whether this is a lockout (429)
const isLockedOut = shallowRef(false)

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
  if (!canSubmit.value || !validate()) return
  loading.value = true
  clearErrors()

  try {
    const user = await login(username.value.trim(), password.value)
    // Prime the session cache so the route guard sees the fresh login
    // instead of the stale "unauthenticated" result from app startup.
    sessionStore.setUser(user)
    await router.replace(await onboardingLoginTarget(route.query.redirect))
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
        bannerError.value =
          err.apiError?.message ?? t('login.errGeneric', { status: err.status })
      }
    } else {
      bannerError.value = t('login.errUnknown')
    }
  } finally {
    loading.value = false
  }
}

const canSubmit = computed(() => !loading.value && !isLockedOut.value)
</script>

<template>
  <div class="login-panel">
    <header class="login-heading">
      <h2 id="login-title">
        {{ t('login.headlineWelcome') }}<br />{{ t('login.headlineEnter') }}
        <span>{{ t('login.headlineWord') }}</span
        >{{ t('login.headlinePunct') }}
      </h2>
    </header>

    <form
      class="login-form"
      :aria-label="t('login.formAria')"
      novalidate
      @submit.prevent="handleSubmit"
    >
      <div
        v-if="bannerError"
        class="banner"
        :class="{ 'banner--locked': isLockedOut }"
        role="alert"
        aria-live="assertive"
      >
        <AlertCircle aria-hidden="true" />
        <span>{{ bannerError }}</span>
      </div>

      <div class="field">
        <label class="field__label" for="username">{{
          t('login.account')
        }}</label>
        <div
          class="input-shell"
          :class="{ 'input-shell--error': usernameError }"
        >
          <User class="input-icon" aria-hidden="true" />
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
        <span
          v-if="usernameError"
          id="username-err"
          class="field__error"
          role="alert"
          >{{ usernameError }}</span
        >
      </div>

      <div class="field">
        <label class="field__label" for="password">{{
          t('login.password')
        }}</label>
        <div
          class="input-shell"
          :class="{ 'input-shell--error': passwordError }"
        >
          <Lock class="input-icon" aria-hidden="true" />
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
            :disabled="loading || isLockedOut"
            :aria-label="
              showPassword ? t('login.hidePassword') : t('login.showPassword')
            "
            :aria-pressed="showPassword"
            aria-controls="password"
            @click="showPassword = !showPassword"
          >
            <EyeOff v-if="showPassword" aria-hidden="true" />
            <Eye v-else aria-hidden="true" />
          </button>
        </div>
        <span
          v-if="passwordError"
          id="password-err"
          class="field__error"
          role="alert"
          >{{ passwordError }}</span
        >
      </div>

      <button
        type="submit"
        class="submit"
        :disabled="!canSubmit"
        :aria-busy="loading"
      >
        <span v-if="loading" class="spinner" aria-hidden="true" />
        <span>{{ loading ? t('login.connecting') : t('login.enter') }}</span>
        <ArrowRight v-if="!loading" aria-hidden="true" />
      </button>
    </form>

    <PlatformLoginEntry />
  </div>
</template>

<style scoped>
.login-panel {
  width: min(100%, 376px);
  margin: auto;
  padding-block: 28px;
}

.login-heading {
  margin-bottom: 34px;
}
.login-heading h2 span {
  color: var(--lp-accent);
}
.login-heading h2 {
  margin: 0 0 12px;
  color: var(--lp-text);
  font-size: 32px;
  font-weight: 650;
  letter-spacing: -0.035em;
  line-height: 1.25;
}

.login-form {
  display: flex;
  flex-direction: column;
  gap: 23px;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 9px;
}
.field__label {
  color: var(--lp-label);
  font-size: 13px;
  font-weight: 500;
}
.input-shell {
  display: flex;
  align-items: center;
  min-height: 52px;
  padding-left: 16px;
  border: 1px solid var(--lp-control-border);
  border-radius: 9px;
  background: var(--lp-control-bg);
  transition:
    border-color 180ms,
    box-shadow 180ms,
    background-color 180ms;
}
.input-shell:hover {
  border-color: var(--lp-control-hover-border);
}
.input-shell:focus-within {
  border-color: var(--lp-focus);
  background: var(--lp-focus-bg);
  box-shadow: 0 0 0 3px rgb(68 126 247 / 12%);
}
.input-icon {
  flex: 0 0 18px;
  width: 18px;
  height: 18px;
  color: var(--lp-muted);
}
.field__input {
  min-width: 0;
  width: 100%;
  min-height: 50px;
  padding: 12px;
  border: 0;
  outline: none;
  background: transparent;
  color: var(--lp-text);
  font: inherit;
  font-size: 14px;
  caret-color: var(--lp-focus);
}
.field__input::placeholder {
  color: var(--lp-placeholder);
  opacity: 1;
}
.field__input:disabled {
  cursor: not-allowed;
  opacity: 0.65;
}
.field__input:-webkit-autofill {
  -webkit-text-fill-color: var(--lp-text);
  box-shadow: 0 0 0 1000px var(--lp-control-bg) inset;
  border-radius: 8px;
}
.reveal {
  display: grid;
  flex: 0 0 44px;
  place-items: center;
  width: 44px;
  height: 44px;
  margin-right: 3px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--lp-muted);
  cursor: pointer;
}
.reveal svg {
  width: 19px;
  height: 19px;
}
.reveal:hover:not(:disabled) {
  color: var(--lp-text);
  background: var(--lp-hover-bg);
}
.reveal:focus-visible {
  outline: 2px solid var(--lp-focus);
  outline-offset: -3px;
}
.reveal:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
.input-shell--error,
.input-shell--error:focus-within {
  border-color: var(--color-red);
}
.field__error {
  color: var(--color-red);
  font-size: 12px;
  line-height: 1.6;
}
.banner {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  border: 1px solid var(--color-red-line);
  border-radius: 8px;
  background: var(--color-red-soft);
  color: var(--color-red);
  font-size: 13px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.banner svg {
  flex: 0 0 18px;
  width: 18px;
  height: 18px;
  margin-top: 2px;
}
.banner--locked {
  border-color: var(--lp-warning-border);
  background: var(--lp-warning-bg);
  color: var(--lp-warning-text);
}
.submit {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  width: 100%;
  min-height: 52px;
  margin-top: 6px;
  padding: 12px 24px;
  border: 1px solid #6495fb;
  border-radius: 9px;
  background: var(--lp-button-bg);
  box-shadow:
    0 4px 24px rgb(38 102 234 / 18%),
    inset 0 1px 0 rgb(255 255 255 / 15%);
  color: #fff;
  font: inherit;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition:
    background-color 180ms,
    box-shadow 180ms,
    transform 180ms;
}
.submit svg {
  position: absolute;
  right: 19px;
  width: 19px;
  height: 19px;
  transition: transform 180ms;
}
.submit:hover:not(:disabled) {
  background: var(--lp-button-hover);
  box-shadow: 0 6px 30px rgb(38 102 234 / 32%);
}
.submit:hover:not(:disabled) svg {
  transform: translateX(3px);
}
.submit:active:not(:disabled) {
  transform: translateY(1px);
}
.submit:focus-visible {
  outline: 2px solid var(--lp-focus);
  outline-offset: 4px;
}
.submit:disabled {
  cursor: not-allowed;
  opacity: 0.65;
  box-shadow: none;
}
.spinner {
  width: 17px;
  height: 17px;
  border: 2px solid rgb(255 255 255 / 30%);
  border-top-color: #fff;
  border-radius: 50%;
  animation: spin 700ms linear infinite;
}
/* Platform authorization stays in normal document flow, including errors and callbacks. */
.login-panel :deep(.platform-entry) {
  position: static;
  margin-top: 27px;
}
.login-panel :deep(.platform-divider) {
  margin-bottom: 14px;
  color: var(--lp-muted);
  font-size: 12px;
}
.login-panel :deep(.platform-button) {
  min-height: 54px;
  border-color: var(--lp-control-border);
  border-radius: 8px;
  background: var(--lp-control-bg);
  backdrop-filter: none;
}
.login-panel :deep(.platform-button:hover:not(:disabled)) {
  border-color: var(--lp-control-hover-border);
  background: var(--lp-hover-bg);
}

/* Authorization controls and transient messages follow the same palette. */
.login-panel :deep(.platform-divider::before),
.login-panel :deep(.platform-divider::after) {
  background: var(--lp-control-border);
}
.login-panel :deep(.platform-button:active:not(:disabled)) {
  background: var(--lp-hover-bg);
}
.login-panel :deep(.platform-note) {
  border-color: var(--lp-control-border);
  background: var(--lp-hover-bg);
  color: var(--lp-text);
}
.login-panel :deep(.platform-dismiss) {
  color: var(--lp-muted);
}
.login-panel :deep(.platform-link) {
  color: var(--lp-accent);
}
.login-panel :deep(.platform-spinner) {
  border-color: var(--lp-control-border);
  border-top-color: var(--lp-text);
}
.login-panel :deep(.platform-button:focus-visible),
.login-panel :deep(.platform-link:focus-visible),
.login-panel :deep(.platform-dismiss:focus-visible) {
  outline-color: var(--lp-focus);
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
@media (max-width: 760px) {
  .login-panel {
    max-width: 400px;
    padding-block: 0;
  }

  .login-heading {
    margin-bottom: 28px;
  }
  .login-heading h2 {
    font-size: 29px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .input-shell,
  .submit,
  .submit svg {
    transition: none;
  }
  .spinner {
    animation: none;
  }
}

@media (max-height: 800px) and (min-width: 761px) {
  .login-panel {
    padding-block: 12px;
  }

  .login-heading {
    margin-bottom: 26px;
  }
  .login-form {
    gap: 19px;
  }
}
</style>
