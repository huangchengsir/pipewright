<script setup lang="ts">
import LoginShowcase from '../components/auth/LoginShowcase.vue'
import LoginForm from '../components/auth/LoginForm.vue'
import BrandIcon from '../components/BrandIcon.vue'
import ThemeToggle from '../components/ThemeToggle.vue'
import { useThemeStore } from '../stores/theme'

const themeStore = useThemeStore()
</script>

<template>
  <main class="login-page" :data-theme="themeStore.current">
    <LoginShowcase />
    <section class="login-access" aria-labelledby="login-title">
      <div class="login-access-brand" role="img" aria-label="Pipewright">
        <BrandIcon class="access-brand-icon" />
      </div>
      <LoginForm />
    </section>
    <ThemeToggle />
  </main>
</template>

<style scoped>
/* Login colors follow the existing application theme and persisted preference. */
.login-page {
  --lp-text: #eef4ff;
  --lp-dim: #9aabc4;
  --lp-muted: #8d9fba;
  --lp-accent: #79a8ff;
  --lp-label: #d4deef;
  --lp-bg: #080e1a;
  --lp-showcase-bg: #080f1d;
  --lp-access-bg: #0c1423;
  --lp-border: #1b2940;
  --lp-control-bg: #111c2d;
  --lp-control-border: #2a3850;
  --lp-control-hover-border: #5272a3;
  --lp-hover-bg: #192940;
  --lp-focus-bg: #121f34;
  --lp-focus: #5b91fb;
  --lp-placeholder: #8090a9;
  --lp-description: #a6bad9;
  --lp-button-bg: #3978f6;
  --lp-button-hover: #4986ff;
  --lp-grid-line: rgb(110 151 223 / 22%);
  --lp-glow: rgb(24 78 163 / 20%);
  --lp-warning-bg: #2b251c;
  --lp-warning-border: #6e5835;
  --lp-warning-text: #f5d296;
  color-scheme: dark;
  display: grid;
  grid-template-columns: minmax(0, 4fr) minmax(0, 6fr);
  min-height: 100vh;
  min-height: 100dvh;
  background: var(--lp-bg);
  color: var(--lp-text);
  font-family: var(--font-sans);
  isolation: isolate;
}
.login-page[data-theme='light'] {
  --lp-text: #1c2d46;
  --lp-dim: #526680;
  --lp-muted: #62758e;
  --lp-accent: #2563eb;
  --lp-label: #344864;
  --lp-bg: #f4f7fc;
  --lp-showcase-bg: #eef4fc;
  --lp-access-bg: #ffffff;
  --lp-border: #d7e1ef;
  --lp-control-bg: #f8fafd;
  --lp-control-border: #c9d5e5;
  --lp-control-hover-border: #8ca7cf;
  --lp-hover-bg: #edf3fc;
  --lp-focus-bg: #ffffff;
  --lp-focus: #2563eb;
  --lp-placeholder: #64758b;
  --lp-description: #435c7e;
  --lp-button-bg: #2563eb;
  --lp-button-hover: #1d4ed8;
  --lp-grid-line: rgb(59 103 171 / 16%);
  --lp-glow: rgb(73 133 235 / 10%);
  --lp-warning-bg: #fff8e8;
  --lp-warning-border: #d8b363;
  --lp-warning-text: #805100;
  color-scheme: light;
}
.login-access {
  display: flex;
  align-items: center;
  min-width: 0;
  padding: 92px clamp(32px, 5.2vw, 90px) 60px;
  border-left: 1px solid var(--lp-border);
  background:
    radial-gradient(ellipse at 100% 0%, rgb(28 56 105 / 10%), transparent 60%),
    var(--lp-access-bg);
}
.login-access-brand {
  display: none;
}
.access-brand-icon {
  width: 100%;
  height: 100%;
}
@media (max-width: 1024px) {
  .login-page {
    grid-template-columns: minmax(0, 1fr);
  }
  .login-access {
    padding: 96px 32px 48px;
    border-left: 0;
  }
  .login-access-brand {
    position: fixed;
    top: 12px;
    left: 24px;
    z-index: 10;
    display: block;
    width: 40px;
    height: 40px;
  }
}
@media (max-width: 760px) {
  .login-access {
    padding: 96px 24px 32px;
  }
}

@media (max-height: 800px) and (min-width: 1025px) {
  .login-access {
    padding-block: 72px 34px;
  }
}
</style>
