<script setup lang="ts">
import { Package, Server, ShieldLock } from '@vicons/tabler'
import { computed, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import BrandIcon from '../BrandIcon.vue'
import LoginGridBackground from './LoginGridBackground.vue'

const { t } = useI18n()
const content = useTemplateRef<HTMLDivElement>('content')
const footer = useTemplateRef<HTMLElement>('footer')
const protectedElements = computed(() => (footer.value ? [footer.value] : []))
</script>

<template>
  <section class="showcase" aria-labelledby="showcase-title">
    <LoginGridBackground
      :content="content"
      :protected-elements="protectedElements"
    />
    <div class="showcase-glow" aria-hidden="true" />

    <div ref="content" class="showcase-content">
      <div class="project-brand">
        <BrandIcon class="brand-mark" />
        <h1 id="showcase-title" class="brand-word">Pipe<span>wright</span></h1>
      </div>
      <p class="project-description">{{ t('login.lede') }}</p>
    </div>

    <footer ref="footer" class="showcase-footer">
      <span class="trust-item">
        <Server class="trust-icon" aria-hidden="true" focusable="false" />
        <span>{{ t('login.trustSelfHosted') }}</span>
      </span>
      <span class="trust-item">
        <ShieldLock class="trust-icon" aria-hidden="true" focusable="false" />
        <span>{{ t('login.trustNoEgress') }}</span>
      </span>
      <span class="trust-item">
        <Package class="trust-icon" aria-hidden="true" focusable="false" />
        <span>{{ t('login.trustSingleBinary') }}</span>
      </span>
    </footer>
  </section>
</template>

<style scoped>
.showcase {
  position: relative;
  isolation: isolate;
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 48px clamp(24px, 2.4vw, 48px) 34px;
  overflow: hidden;
  background: var(--lp-showcase-bg);
}
.showcase-glow {
  position: absolute;
  z-index: -1;
  left: -45%;
  top: 17%;
  width: 160%;
  height: 90%;
  pointer-events: none;
  background: radial-gradient(
    ellipse at center,
    var(--lp-glow),
    transparent 65%
  );
}
.showcase-content {
  width: 100%;
  max-width: 640px;
  margin-block: auto;
  padding-block: 80px;
}
.project-brand {
  display: flex;
  align-items: center;
  gap: 12px;
}
.brand-mark {
  display: block;
  flex: 0 0 48px;
  width: 48px;
  height: 48px;
  aspect-ratio: 1;
}
.brand-word {
  margin: 0;
  color: var(--lp-text);
  font-size: clamp(30px, 2.6vw, 48px);
  font-weight: 650;
  letter-spacing: -0.045em;
  line-height: 1.2;
}
.brand-word > span {
  font-weight: 400;
}
.project-description {
  max-width: 520px;
  margin: 30px 0 0;
  color: var(--lp-description);
  font-size: clamp(17px, 1.5vw, 21px);
  line-height: 1.9;
}
.showcase-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px 24px;
  color: var(--lp-muted);
  font-size: 12px;
}
.trust-item {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  white-space: nowrap;
}
.trust-icon {
  flex: 0 0 16px;
  width: 16px;
  height: 16px;
}
@media (max-width: 1024px) {
  .showcase {
    display: none;
  }
}
</style>
