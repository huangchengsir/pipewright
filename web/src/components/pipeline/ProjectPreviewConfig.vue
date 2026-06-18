<script setup lang="ts">
/*
  ProjectPreviewConfig.vue — 每项目「PR 预览环境」配置卡(R4 / E4.1)。

  嵌在流水线编辑器的「触发设置」tab。开启后,每个 PR 运行会自动拉起一个临时环境:
  在所选 DNS 提供商的根域下生成 pr-N-<proj>.<根域>、签证书、绑定到该次部署的容器;
  PR 关闭或手动回收时拆除。对标 Vercel / Netlify 的预览部署,自托管版。

  - 自加载:挂载即拉 GET preview-config + DNS 提供商列表。
  - 总开关 + DNS 提供商下拉 + 根域(FQDN 校验)。开启需选定提供商 + 合法根域。
  - 保存调 PUT preview-config;乐观提示 + 失败回滚。
  本卡只持有编辑缓冲,保存成功才视为生效。
*/
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NIcon } from 'naive-ui'
import { Rocket, World } from '@vicons/tabler'
import {
  getPreviewConfig,
  setPreviewConfig,
  type PreviewConfig,
} from '../../api/previewEnvs'
import { listDnsProviders, type DnsProvider } from '../../api/dnsProviders'
import { HttpError } from '../../api/http'
import { useToast } from '../../composables/useToast'

const props = defineProps<{
  projectId: string
}>()

const { t } = useI18n()
const toast = useToast()

// ─── 状态 ──────────────────────────────────────────────────────────────────────
type LoadState = 'idle' | 'loading' | 'error'
const loadState = ref<LoadState>('idle')
const loadError = ref('')

const enabled = ref(false)
const dnsProviderId = ref('')
const baseDomain = ref('')
const providers = ref<DnsProvider[]>([])

const FQDN_RE = /^(?=.{1,253}$)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i
const baseDomainValid = computed(() => FQDN_RE.test(baseDomain.value.trim().toLowerCase()))
const selectedProvider = computed(() => providers.value.find((p) => p.id === dnsProviderId.value))

// 开启预览必须:选定提供商 + 合法根域。关闭态不校验(可随时保存关闭)。
const enabledConfigInvalid = computed(
  () => enabled.value && (dnsProviderId.value.length === 0 || !baseDomainValid.value),
)

async function load(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    const [cfg, provs] = await Promise.all([
      getPreviewConfig(props.projectId),
      listDnsProviders().catch(() => [] as DnsProvider[]),
    ])
    enabled.value = cfg.enabled
    dnsProviderId.value = cfg.dnsProviderId
    baseDomain.value = cfg.baseDomain
    providers.value = provs
    loadState.value = 'idle'
  } catch (err) {
    loadState.value = 'error'
    loadError.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? t('previewEnvs.config.errLoad', { status: err.status }))
        : t('previewEnvs.config.errNetwork')
  }
}

onMounted(load)
watch(() => props.projectId, load)

// 选定提供商时,若根域还空着,顺手填上提供商根域(常见就是它)。
watch(dnsProviderId, () => {
  if (selectedProvider.value && !baseDomain.value.trim()) {
    baseDomain.value = selectedProvider.value.baseDomain
  }
})

// ─── 保存 ──────────────────────────────────────────────────────────────────────
const saving = ref(false)
const canSave = computed(() => !saving.value && !enabledConfigInvalid.value && loadState.value === 'idle')

async function save(): Promise<void> {
  if (!canSave.value) return
  saving.value = true
  try {
    const body: PreviewConfig = {
      projectId: props.projectId,
      enabled: enabled.value,
      dnsProviderId: dnsProviderId.value,
      baseDomain: baseDomain.value.trim().toLowerCase(),
    }
    const updated = await setPreviewConfig(props.projectId, body)
    enabled.value = updated.enabled
    dnsProviderId.value = updated.dnsProviderId
    baseDomain.value = updated.baseDomain
    toast.success(t('previewEnvs.config.saved'))
  } catch (err) {
    toast.error(t('previewEnvs.config.saveFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('previewEnvs.config.errReq', { status: err.status }))
          : t('previewEnvs.config.errNetwork'),
    })
  } finally {
    saving.value = false
  }
}

// 例:pr-42-myproj.preview.example.com —— 让用户直观看到链接形态。
const examplePreview = computed(() => {
  const base = baseDomain.value.trim().toLowerCase() || 'preview.example.com'
  return `pr-42-app.${base}`
})
</script>

<template>
  <section class="pvc">
    <header class="pvc__head">
      <div class="pvc__icon" aria-hidden="true"><NIcon :size="18"><Rocket /></NIcon></div>
      <div class="pvc__head-text">
        <h3 class="pvc__title">{{ t('previewEnvs.config.title') }}</h3>
        <p class="pvc__sub">{{ t('previewEnvs.config.subtitle') }}</p>
      </div>
      <span class="pvc__badge" :class="enabled ? 'pvc__badge--on' : 'pvc__badge--off'">
        {{ enabled ? t('previewEnvs.config.on') : t('previewEnvs.config.off') }}
      </span>
    </header>

    <div v-if="loadState === 'loading'" class="pvc__state">{{ t('previewEnvs.config.loading') }}</div>
    <div v-else-if="loadState === 'error'" class="pvc__banner" role="alert">
      <span>⚠ {{ loadError }}</span>
      <button class="pvc__retry" @click="load">↻ {{ t('previewEnvs.config.retry') }}</button>
    </div>

    <div v-else class="pvc__body">
      <!-- 总开关 -->
      <label class="pvc__toggle">
        <input v-model="enabled" type="checkbox" class="pvc__cb" />
        <span class="pvc__box" aria-hidden="true" />
        <span class="pvc__toggle-txt">
          <span class="pvc__toggle-name">{{ t('previewEnvs.config.enableLabel') }}</span>
          <span class="pvc__toggle-desc">{{ t('previewEnvs.config.enableDesc') }}</span>
        </span>
      </label>

      <!-- 提供商 + 根域(开启时显示;关闭也保留,便于回看) -->
      <div class="pvc__grid">
        <div class="pvc__field">
          <label class="pvc__lbl">{{ t('previewEnvs.config.providerLabel') }}</label>
          <select v-model="dnsProviderId" class="pvc__in">
            <option value="">{{ t('previewEnvs.config.providerNone') }}</option>
            <option v-for="p in providers" :key="p.id" :value="p.id">{{ p.name }} · {{ p.baseDomain }}</option>
          </select>
          <p v-if="providers.length === 0" class="pvc__hint">{{ t('previewEnvs.config.noProviders') }}</p>
        </div>

        <div class="pvc__field">
          <label class="pvc__lbl">{{ t('previewEnvs.config.baseDomainLabel') }}</label>
          <input
            v-model="baseDomain"
            class="pvc__in mono"
            :class="{ 'pvc__in--bad': enabled && baseDomain.trim().length > 0 && !baseDomainValid }"
            :placeholder="t('previewEnvs.config.baseDomainPlaceholder')"
            autocomplete="off"
            spellcheck="false"
          />
        </div>
      </div>

      <!-- 链接形态预览 -->
      <p class="pvc__preview">
        <NIcon :size="13" class="pvc__preview-ic"><World /></NIcon>
        {{ t('previewEnvs.config.exampleLabel') }}
        <code class="pvc__preview-url mono">https://{{ examplePreview }}</code>
      </p>

      <p v-if="enabledConfigInvalid" class="pvc__hint pvc__hint--err">{{ t('previewEnvs.config.invalidHint') }}</p>

      <div class="pvc__foot">
        <span class="pvc__foot-note">{{ t('previewEnvs.config.footNote') }}</span>
        <button class="pvc__save" :disabled="!canSave" @click="save">
          {{ saving ? t('previewEnvs.config.saving') : t('previewEnvs.config.save') }}
        </button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.pvc {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  background: var(--color-card);
  box-shadow: var(--shadow);
  overflow: hidden;
}
.pvc__head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 16px 18px;
  border-bottom: 1px solid var(--color-border);
}
.pvc__icon {
  width: 36px;
  height: 36px;
  flex-shrink: 0;
  border-radius: var(--rounded-lg);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  display: grid;
  place-items: center;
}
.pvc__head-text {
  flex: 1;
  min-width: 0;
}
.pvc__title {
  margin: 2px 0 0;
  font-size: 1rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  color: var(--color-text);
}
.pvc__sub {
  margin: 4px 0 0;
  font-size: 0.8rem;
  color: var(--color-faint);
  line-height: 1.5;
  max-width: 64ch;
}
.pvc__badge {
  flex-shrink: 0;
  font-size: var(--text-micro);
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  padding: 4px 10px;
  border-radius: var(--rounded-full);
}
.pvc__badge--on {
  color: var(--color-green);
  background: var(--color-green-soft);
}
.pvc__badge--off {
  color: var(--color-faint);
  background: var(--color-inset);
}

.pvc__state {
  padding: 28px 18px;
  text-align: center;
  font-size: 0.84rem;
  color: var(--color-faint);
}
.pvc__banner {
  display: flex;
  align-items: center;
  gap: 9px;
  margin: 16px 18px;
  padding: 11px 14px;
  border-radius: var(--rounded);
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  color: var(--color-red);
  font-size: 0.83rem;
}
.pvc__retry {
  margin-left: auto;
  background: none;
  border: none;
  color: var(--color-red);
  font-weight: 600;
  cursor: pointer;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.pvc__body {
  padding: 18px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 总开关 */
.pvc__toggle {
  display: flex;
  align-items: flex-start;
  gap: 11px;
  cursor: pointer;
}
.pvc__cb {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
}
.pvc__box {
  flex-shrink: 0;
  margin-top: 1px;
  width: 40px;
  height: 22px;
  border-radius: var(--rounded-full);
  background: var(--color-border-strong);
  position: relative;
  transition: background var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.pvc__box::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px oklch(0% 0 0 / 0.35);
  transition: left var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.pvc__cb:checked + .pvc__box {
  background: var(--color-primary);
}
.pvc__cb:checked + .pvc__box::after {
  left: 20px;
}
.pvc__cb:focus-visible + .pvc__box {
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.pvc__toggle-txt {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.pvc__toggle-name {
  font-size: 0.88rem;
  font-weight: 600;
  color: var(--color-text);
}
.pvc__toggle-desc {
  font-size: var(--text-micro);
  color: var(--color-faint);
  line-height: 1.45;
}

.pvc__grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
.pvc__field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}
.pvc__lbl {
  font-size: 0.78rem;
  font-weight: 500;
  color: var(--color-dim);
}
.pvc__in {
  width: 100%;
  height: 38px;
  padding: 0 12px;
  border-radius: var(--rounded);
  border: 1px solid var(--color-border);
  background: var(--color-inset);
  color: var(--color-text);
  font-size: 0.86rem;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}
.pvc__in:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.pvc__in--bad {
  border-color: var(--color-red);
}
.mono {
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

.pvc__preview {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 7px;
  margin: 0;
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.pvc__preview-ic {
  color: var(--color-primary);
  flex-shrink: 0;
}
.pvc__preview-url {
  padding: 2px 8px;
  border-radius: var(--rounded-md);
  background: var(--color-inset);
  color: var(--color-primary);
  border: 1px solid var(--color-border);
}

.pvc__hint {
  margin: 0;
  font-size: var(--text-micro);
  color: var(--color-faint);
  line-height: 1.45;
}
.pvc__hint--err {
  color: var(--color-red);
}

.pvc__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
  padding-top: 4px;
  border-top: 1px solid var(--color-border);
}
.pvc__foot-note {
  font-size: var(--text-micro);
  color: var(--color-faint);
  line-height: 1.5;
  max-width: 48ch;
}
.pvc__save {
  flex-shrink: 0;
  font-size: var(--text-label);
  font-weight: 600;
  padding: 8px 18px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-primary);
  background: var(--color-primary);
  color: #fff;
  cursor: pointer;
  transition: background var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.pvc__save:hover:not(:disabled) {
  background: var(--color-primary-press);
}
.pvc__save:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

@media (max-width: 560px) {
  .pvc__grid {
    grid-template-columns: 1fr;
  }
}
</style>
