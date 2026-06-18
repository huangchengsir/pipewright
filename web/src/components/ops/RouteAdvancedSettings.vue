<script setup lang="ts">
/*
  RouteAdvancedSettings.vue — 单条路由的「高级设置」折叠区(R2 / FR-6..FR-9)。

  嵌在 ReverseProxyPanel 的每张路由卡片底部,折叠默认收起。覆盖四块:
  - 多域名别名(aliases):tag 输入,逐个 FQDN 客户端校验,可加/删。
  - 访问控制:Basic Auth(用户名 + 写-only 密码,只显示「已设置/未设置」,从不回显存量密码;
    留空密码=保持原密码;清空用户名=关闭认证)+ IP 允许/拒绝名单(CIDR chips,客户端 CIDR 校验)。
  - 安全加固:强制 HTTPS / HSTS / 安全响应头 / 压缩 四个开关。
  - 重定向:可编辑的 {from, to, status∈301/302/307/308} 列表。

  「保存高级设置」调 updateProxyRoute;成功后 emit('saved', 新路由)让父替换。
  本组件只持有编辑缓冲(从 props.route.config 深拷贝),保存成功才回写父。
*/
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NIcon } from 'naive-ui'
import { ChevronRight, Plus, X, Trash, ShieldLock, World, ArrowRight } from '@vicons/tabler'
import {
  updateProxyRoute,
  type ProxyRoute,
  type ProxyRouteConfig,
  type Redirect,
  type RedirectStatus,
} from '../../api/reverseProxy'
import { HttpError } from '../../api/http'
import { useToast } from '../../composables/useToast'

const props = defineProps<{
  route: ProxyRoute
}>()

const emit = defineEmits<{
  /** 保存成功:把后端返回的更新后路由交给父替换。 */
  (e: 'saved', updated: ProxyRoute): void
}>()

const { t } = useI18n()
const toast = useToast()

const open = ref(false)

// ─── 编辑缓冲(从 route.config 深拷贝;路由变化时重置) ───────────────────────────
const aliases = ref<string[]>([])
const forceHttps = ref(false)
const hsts = ref(false)
const securityHeaders = ref(false)
const compression = ref(false)
const basicAuthUser = ref('')
const basicAuthHadPassword = ref(false) // 后端报告的「已设置密码」状态
const basicAuthPassword = ref('') // 写-only 新密码缓冲(空=保持)
const ipAllow = ref<string[]>([])
const ipDeny = ref<string[]>([])
const redirects = ref<Redirect[]>([])

function resetFromRoute(): void {
  const c = props.route.config
  aliases.value = [...c.aliases]
  forceHttps.value = c.forceHttps
  hsts.value = c.hsts
  securityHeaders.value = c.securityHeaders
  compression.value = c.compression
  basicAuthUser.value = c.basicAuthUser
  basicAuthHadPassword.value = c.basicAuthEnabled
  basicAuthPassword.value = ''
  ipAllow.value = [...c.ipAllow]
  ipDeny.value = [...c.ipDeny]
  redirects.value = c.redirects.map((r) => ({ ...r }))
}

// 路由对象(尤其 config)变化时重置缓冲;首次也跑一遍。
watch(() => props.route, resetFromRoute, { immediate: true, deep: true })

// ─── 校验工具 ─────────────────────────────────────────────────────────────────
const FQDN_RE = /^(?=.{1,253}$)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i
function isFqdn(s: string): boolean {
  return FQDN_RE.test(s.trim().toLowerCase())
}

// CIDR:a.b.c.d/0-32 或 IPv6/0-128。逐段范围校验,松紧适中(后端会再严格校验)。
function isCidr(s: string): boolean {
  const v = s.trim()
  const slash = v.indexOf('/')
  if (slash < 0) return false
  const addr = v.slice(0, slash)
  const bits = Number(v.slice(slash + 1))
  if (!Number.isInteger(bits)) return false
  if (addr.includes(':')) {
    // 粗略 IPv6:仅十六进制段与 ::,前缀 0-128。
    return bits >= 0 && bits <= 128 && /^[0-9a-f:]+$/i.test(addr) && addr.includes(':')
  }
  const parts = addr.split('.')
  if (parts.length !== 4) return false
  if (!parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255)) return false
  return bits >= 0 && bits <= 32
}

// ─── 别名 tag 输入 ────────────────────────────────────────────────────────────
const aliasDraft = ref('')
const aliasError = computed(() => {
  const v = aliasDraft.value.trim().toLowerCase()
  if (!v) return ''
  if (!isFqdn(v)) return t('reverseProxy.adv.aliasInvalid')
  if (v === props.route.domain.toLowerCase()) return t('reverseProxy.adv.aliasIsPrimary')
  if (aliases.value.some((a) => a.toLowerCase() === v)) return t('reverseProxy.adv.aliasDup')
  return ''
})
function addAlias(): void {
  const v = aliasDraft.value.trim().toLowerCase()
  if (!v || aliasError.value) return
  aliases.value = [...aliases.value, v]
  aliasDraft.value = ''
}
function removeAlias(a: string): void {
  aliases.value = aliases.value.filter((x) => x !== a)
}

// ─── IP 名单 chip 输入(allow / deny 共用一套逻辑) ───────────────────────────────
const allowDraft = ref('')
const denyDraft = ref('')
const allowError = computed(() => cidrDraftError(allowDraft.value, ipAllow.value))
const denyError = computed(() => cidrDraftError(denyDraft.value, ipDeny.value))
function cidrDraftError(draft: string, list: string[]): string {
  const v = draft.trim().toLowerCase()
  if (!v) return ''
  if (!isCidr(v)) return t('reverseProxy.adv.cidrInvalid')
  if (list.some((x) => x.toLowerCase() === v)) return t('reverseProxy.adv.cidrDup')
  return ''
}
function addAllow(): void {
  const v = allowDraft.value.trim().toLowerCase()
  if (!v || allowError.value) return
  ipAllow.value = [...ipAllow.value, v]
  allowDraft.value = ''
}
function addDeny(): void {
  const v = denyDraft.value.trim().toLowerCase()
  if (!v || denyError.value) return
  ipDeny.value = [...ipDeny.value, v]
  denyDraft.value = ''
}
function removeAllow(c: string): void {
  ipAllow.value = ipAllow.value.filter((x) => x !== c)
}
function removeDeny(c: string): void {
  ipDeny.value = ipDeny.value.filter((x) => x !== c)
}

// ─── 重定向 ───────────────────────────────────────────────────────────────────
const REDIRECT_STATUSES: RedirectStatus[] = [301, 302, 307, 308]
function addRedirect(): void {
  redirects.value = [...redirects.value, { from: '', to: '', status: 301 }]
}
function removeRedirect(i: number): void {
  redirects.value = redirects.value.filter((_, idx) => idx !== i)
}

// 任一重定向行只填了一半(from/to 缺一)→ 视为无效,禁用保存。
const redirectIncomplete = computed(() =>
  redirects.value.some((r) => Boolean(r.from.trim()) !== Boolean(r.to.trim())),
)

// ─── Basic Auth 派生状态 ──────────────────────────────────────────────────────
const authUserCleared = computed(() => basicAuthUser.value.trim().length === 0)
// 启用认证但既无存量密码、又没输入新密码 → 不合法(空密码无意义)。
const authNeedsPassword = computed(
  () => !authUserCleared.value && !basicAuthHadPassword.value && basicAuthPassword.value.length === 0,
)
const passwordStateLabel = computed(() =>
  basicAuthHadPassword.value ? t('reverseProxy.adv.pwdSet') : t('reverseProxy.adv.pwdUnset'),
)

// ─── 保存 ─────────────────────────────────────────────────────────────────────
const saving = ref(false)
const canSave = computed(
  () => !saving.value && !redirectIncomplete.value && !authNeedsPassword.value,
)

async function save(): Promise<void> {
  if (!canSave.value) return
  // 清空用户名 = 关闭认证:不发密码,用户名发空。
  const user = authUserCleared.value ? '' : basicAuthUser.value.trim()
  const config: Omit<ProxyRouteConfig, 'basicAuthEnabled'> = {
    aliases: aliases.value.map((a) => a.toLowerCase()),
    forceHttps: forceHttps.value,
    hsts: hsts.value,
    securityHeaders: securityHeaders.value,
    compression: compression.value,
    basicAuthUser: user,
    ipAllow: ipAllow.value.map((c) => c.toLowerCase()),
    ipDeny: ipDeny.value.map((c) => c.toLowerCase()),
    // 完整填写的重定向行才提交。
    redirects: redirects.value.filter((r) => r.from.trim() && r.to.trim()),
  }
  saving.value = true
  try {
    const updated = await updateProxyRoute(props.route.id, {
      config,
      // 仅在「启用认证 + 输入了新密码」时携带密码;关闭认证不发。
      ...(user && basicAuthPassword.value.length > 0
        ? { basicAuthPassword: basicAuthPassword.value }
        : {}),
    })
    emit('saved', updated)
    toast.success(t('reverseProxy.adv.saved'), { detail: updated.domain })
    open.value = false
  } catch (err) {
    toast.error(t('reverseProxy.adv.saveFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('reverseProxy.errReq', { status: err.status }))
          : t('reverseProxy.errNetwork'),
    })
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="adv">
    <button
      class="adv__toggle"
      :aria-expanded="open"
      :title="open ? t('reverseProxy.adv.collapse') : t('reverseProxy.adv.expand')"
      @click="open = !open"
    >
      <span class="adv__caret" :class="{ 'adv__caret--open': open }">
        <NIcon :size="14"><ChevronRight /></NIcon>
      </span>
      <NIcon :size="14" class="adv__toggle-ic"><ShieldLock /></NIcon>
      <span>{{ t('reverseProxy.adv.title') }}</span>
    </button>

    <div v-if="open" class="adv__body">
      <!-- 多域名别名 -->
      <section class="advsec">
        <h4 class="advsec__h">{{ t('reverseProxy.adv.aliasesTitle') }}</h4>
        <p class="advsec__lede">{{ t('reverseProxy.adv.aliasesLede') }}</p>
        <div class="chips">
          <span v-for="a in aliases" :key="a" class="chip mono">
            {{ a }}
            <button class="chip__x" :aria-label="t('reverseProxy.adv.removeAlias', { v: a })" @click="removeAlias(a)">
              <NIcon :size="11"><X /></NIcon>
            </button>
          </span>
          <span v-if="aliases.length === 0" class="chips__empty">{{ t('reverseProxy.adv.noAliases') }}</span>
        </div>
        <div class="taginput">
          <input
            v-model="aliasDraft"
            class="advin mono"
            :class="{ 'advin--bad': Boolean(aliasError) }"
            :placeholder="t('reverseProxy.adv.aliasPlaceholder')"
            autocomplete="off"
            spellcheck="false"
            @keyup.enter="addAlias"
          />
          <button class="advadd" :disabled="!aliasDraft.trim() || Boolean(aliasError)" @click="addAlias">
            <NIcon :size="13"><Plus /></NIcon>
          </button>
        </div>
        <p v-if="aliasError" class="advhint advhint--err">{{ aliasError }}</p>
      </section>

      <!-- 访问控制 -->
      <section class="advsec">
        <h4 class="advsec__h">{{ t('reverseProxy.adv.accessTitle') }}</h4>

        <!-- Basic Auth -->
        <div class="advsub">
          <span class="advsub__label">{{ t('reverseProxy.adv.basicAuthLabel') }}</span>
          <span class="pwdstate" :class="basicAuthHadPassword ? 'pwdstate--set' : 'pwdstate--unset'">
            {{ passwordStateLabel }}
          </span>
        </div>
        <div class="advgrid2">
          <input
            v-model="basicAuthUser"
            class="advin mono"
            :placeholder="t('reverseProxy.adv.basicAuthUserPlaceholder')"
            autocomplete="off"
            spellcheck="false"
          />
          <input
            v-model="basicAuthPassword"
            type="password"
            class="advin mono"
            :class="{ 'advin--bad': authNeedsPassword }"
            :placeholder="t('reverseProxy.adv.basicAuthPwdPlaceholder')"
            :disabled="authUserCleared"
            autocomplete="new-password"
          />
        </div>
        <p class="advhint">
          {{ authUserCleared ? t('reverseProxy.adv.authClearedHint') : t('reverseProxy.adv.authPwdHint') }}
        </p>
        <p v-if="authNeedsPassword" class="advhint advhint--err">{{ t('reverseProxy.adv.authNeedsPwd') }}</p>

        <!-- IP 允许 -->
        <div class="advsub advsub--gap">
          <span class="advsub__label">{{ t('reverseProxy.adv.ipAllowLabel') }}</span>
        </div>
        <div class="chips">
          <span v-for="c in ipAllow" :key="c" class="chip chip--allow mono">
            {{ c }}
            <button class="chip__x" :aria-label="t('reverseProxy.adv.removeCidr', { v: c })" @click="removeAllow(c)">
              <NIcon :size="11"><X /></NIcon>
            </button>
          </span>
          <span v-if="ipAllow.length === 0" class="chips__empty">{{ t('reverseProxy.adv.ipAllowEmpty') }}</span>
        </div>
        <div class="taginput">
          <input
            v-model="allowDraft"
            class="advin mono"
            :class="{ 'advin--bad': Boolean(allowError) }"
            :placeholder="t('reverseProxy.adv.cidrPlaceholder')"
            autocomplete="off"
            spellcheck="false"
            @keyup.enter="addAllow"
          />
          <button class="advadd" :disabled="!allowDraft.trim() || Boolean(allowError)" @click="addAllow">
            <NIcon :size="13"><Plus /></NIcon>
          </button>
        </div>
        <p v-if="allowError" class="advhint advhint--err">{{ allowError }}</p>

        <!-- IP 拒绝 -->
        <div class="advsub advsub--gap">
          <span class="advsub__label">{{ t('reverseProxy.adv.ipDenyLabel') }}</span>
        </div>
        <div class="chips">
          <span v-for="c in ipDeny" :key="c" class="chip chip--deny mono">
            {{ c }}
            <button class="chip__x" :aria-label="t('reverseProxy.adv.removeCidr', { v: c })" @click="removeDeny(c)">
              <NIcon :size="11"><X /></NIcon>
            </button>
          </span>
          <span v-if="ipDeny.length === 0" class="chips__empty">{{ t('reverseProxy.adv.ipDenyEmpty') }}</span>
        </div>
        <div class="taginput">
          <input
            v-model="denyDraft"
            class="advin mono"
            :class="{ 'advin--bad': Boolean(denyError) }"
            :placeholder="t('reverseProxy.adv.cidrPlaceholder')"
            autocomplete="off"
            spellcheck="false"
            @keyup.enter="addDeny"
          />
          <button class="advadd" :disabled="!denyDraft.trim() || Boolean(denyError)" @click="addDeny">
            <NIcon :size="13"><Plus /></NIcon>
          </button>
        </div>
        <p v-if="denyError" class="advhint advhint--err">{{ denyError }}</p>
      </section>

      <!-- 安全加固 -->
      <section class="advsec">
        <h4 class="advsec__h">{{ t('reverseProxy.adv.hardeningTitle') }}</h4>
        <div class="toggles">
          <label class="tgl">
            <input v-model="forceHttps" type="checkbox" class="tgl__cb" />
            <span class="tgl__box" aria-hidden="true" />
            <span class="tgl__txt">
              <span class="tgl__name">{{ t('reverseProxy.adv.forceHttps') }}</span>
              <span class="tgl__desc">{{ t('reverseProxy.adv.forceHttpsDesc') }}</span>
            </span>
          </label>
          <label class="tgl">
            <input v-model="hsts" type="checkbox" class="tgl__cb" />
            <span class="tgl__box" aria-hidden="true" />
            <span class="tgl__txt">
              <span class="tgl__name">{{ t('reverseProxy.adv.hsts') }}</span>
              <span class="tgl__desc">{{ t('reverseProxy.adv.hstsDesc') }}</span>
            </span>
          </label>
          <label class="tgl">
            <input v-model="securityHeaders" type="checkbox" class="tgl__cb" />
            <span class="tgl__box" aria-hidden="true" />
            <span class="tgl__txt">
              <span class="tgl__name">{{ t('reverseProxy.adv.securityHeaders') }}</span>
              <span class="tgl__desc">{{ t('reverseProxy.adv.securityHeadersDesc') }}</span>
            </span>
          </label>
          <label class="tgl">
            <input v-model="compression" type="checkbox" class="tgl__cb" />
            <span class="tgl__box" aria-hidden="true" />
            <span class="tgl__txt">
              <span class="tgl__name">{{ t('reverseProxy.adv.compression') }}</span>
              <span class="tgl__desc">{{ t('reverseProxy.adv.compressionDesc') }}</span>
            </span>
          </label>
        </div>
      </section>

      <!-- 重定向 -->
      <section class="advsec">
        <h4 class="advsec__h">{{ t('reverseProxy.adv.redirectsTitle') }}</h4>
        <p class="advsec__lede">{{ t('reverseProxy.adv.redirectsLede') }}</p>
        <ul v-if="redirects.length > 0" class="rdr" role="list">
          <li v-for="(r, i) in redirects" :key="i" class="rdr__row">
            <input
              v-model="r.from"
              class="advin mono"
              :placeholder="t('reverseProxy.adv.redirectFromPlaceholder')"
              autocomplete="off"
              spellcheck="false"
            />
            <NIcon :size="13" class="rdr__arrow"><ArrowRight /></NIcon>
            <input
              v-model="r.to"
              class="advin mono"
              :placeholder="t('reverseProxy.adv.redirectToPlaceholder')"
              autocomplete="off"
              spellcheck="false"
            />
            <select v-model.number="r.status" class="advin rdr__status">
              <option v-for="s in REDIRECT_STATUSES" :key="s" :value="s">{{ s }}</option>
            </select>
            <button
              class="rdr__del"
              :aria-label="t('reverseProxy.adv.removeRedirect')"
              :title="t('reverseProxy.adv.removeRedirect')"
              @click="removeRedirect(i)"
            >
              <NIcon :size="14"><Trash /></NIcon>
            </button>
          </li>
        </ul>
        <button class="advghost" @click="addRedirect">
          <NIcon :size="13"><Plus /></NIcon>
          {{ t('reverseProxy.adv.addRedirect') }}
        </button>
        <p v-if="redirectIncomplete" class="advhint advhint--err">{{ t('reverseProxy.adv.redirectIncomplete') }}</p>
      </section>

      <!-- 保存 -->
      <div class="adv__foot">
        <span class="adv__foot-note">
          <NIcon :size="13" class="adv__foot-ic"><World /></NIcon>
          {{ t('reverseProxy.adv.footNote') }}
        </span>
        <button class="advsave" :disabled="!canSave" @click="save">
          {{ saving ? t('reverseProxy.adv.saving') : t('reverseProxy.adv.save') }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.adv {
  border-top: 1px dashed var(--color-border-strong);
}

/* 折叠开关 */
.adv__toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 11px 15px;
  background: transparent;
  border: none;
  font-size: var(--text-micro);
  font-weight: 600;
  color: var(--color-dim);
  cursor: pointer;
  transition: color var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.adv__toggle:hover {
  color: var(--color-text);
}
.adv__toggle-ic {
  color: var(--color-primary);
}
.adv__caret {
  display: inline-flex;
  transition: transform var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.adv__caret--open {
  transform: rotate(90deg);
}

.adv__body {
  padding: 4px 15px 16px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.advsec {
  display: flex;
  flex-direction: column;
  gap: 9px;
}
.advsec__h {
  margin: 0;
  font-size: var(--text-micro);
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.07em;
  color: var(--color-faint);
}
.advsec__lede {
  margin: -2px 0 0;
  font-size: var(--text-micro);
  color: var(--color-faint);
  line-height: 1.5;
}

/* chips（别名 / CIDR） */
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
  align-items: center;
}
.chips__empty {
  font-size: var(--text-micro);
  color: var(--color-faint);
  font-style: italic;
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 5px 4px 10px;
  font-size: var(--text-micro);
  border-radius: var(--rounded-full);
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
  color: var(--color-text);
}
.chip--allow {
  border-color: var(--color-green-line);
  background: var(--color-green-soft);
  color: var(--color-green);
}
.chip--deny {
  border-color: var(--color-red-line);
  background: var(--color-red-soft);
  color: var(--color-red);
}
.chip__x {
  display: grid;
  place-items: center;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  border: none;
  background: transparent;
  color: currentColor;
  opacity: 0.6;
  cursor: pointer;
  transition: opacity var(--duration-fast, 150ms) ease;
}
.chip__x:hover {
  opacity: 1;
}

/* tag/cidr 输入行 */
.taginput {
  display: flex;
  gap: 7px;
}
.advin {
  flex: 1;
  min-width: 0;
  font-size: var(--text-label);
  padding: 7px 10px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
  color: var(--color-text);
  transition:
    border-color var(--duration-fast, 150ms) var(--ease-out-expo, ease),
    box-shadow var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.advin:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.advin:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.advin--bad {
  border-color: var(--color-red-line);
}
.advadd {
  display: grid;
  place-items: center;
  flex-shrink: 0;
  width: 34px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-dim);
  cursor: pointer;
  transition:
    color var(--duration-fast, 150ms) ease,
    border-color var(--duration-fast, 150ms) ease;
}
.advadd:hover:not(:disabled) {
  color: var(--color-primary);
  border-color: var(--color-primary);
}
.advadd:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.advhint {
  margin: 0;
  font-size: var(--text-micro);
  color: var(--color-faint);
  line-height: 1.5;
}
.advhint--err {
  color: var(--color-red);
}

/* Basic Auth */
.advsub {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.advsub--gap {
  margin-top: 4px;
}
.advsub__label {
  font-size: var(--text-micro);
  font-weight: 600;
  color: var(--color-dim);
}
.pwdstate {
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 2px 9px;
  border-radius: var(--rounded-full);
}
.pwdstate--set {
  color: var(--color-green);
  background: var(--color-green-soft);
}
.pwdstate--unset {
  color: var(--color-faint);
  background: var(--color-inset);
}
.advgrid2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}

/* 加固开关 */
.toggles {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}
.tgl {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 10px 12px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border);
  background: var(--color-card-2);
  cursor: pointer;
  transition: border-color var(--duration-fast, 150ms) ease;
}
.tgl:hover {
  border-color: var(--color-border-strong);
}
.tgl__cb {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
}
.tgl__box {
  flex-shrink: 0;
  margin-top: 1px;
  width: 36px;
  height: 20px;
  border-radius: var(--rounded-full);
  background: var(--color-border-strong);
  position: relative;
  transition: background var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.tgl__box::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px oklch(0% 0 0 / 0.35);
  transition: left var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.tgl__cb:checked + .tgl__box {
  background: var(--color-primary);
}
.tgl__cb:checked + .tgl__box::after {
  left: 18px;
}
.tgl__cb:focus-visible + .tgl__box {
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.tgl__txt {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.tgl__name {
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-text);
}
.tgl__desc {
  font-size: var(--text-micro);
  color: var(--color-faint);
  line-height: 1.4;
}

/* 重定向 */
.rdr {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.rdr__row {
  display: grid;
  grid-template-columns: 1fr auto 1fr 78px auto;
  align-items: center;
  gap: 7px;
}
.rdr__arrow {
  color: var(--color-faint);
}
.rdr__status {
  flex: none;
  width: 78px;
  cursor: pointer;
}
.rdr__del {
  display: grid;
  place-items: center;
  padding: 7px;
  border-radius: var(--rounded-md);
  border: none;
  background: transparent;
  color: var(--color-faint);
  cursor: pointer;
  transition:
    color var(--duration-fast, 150ms) ease,
    background var(--duration-fast, 150ms) ease;
}
.rdr__del:hover {
  color: var(--color-red);
  background: var(--color-red-soft);
}
.advghost {
  align-self: flex-start;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 6px 12px;
  border-radius: var(--rounded-md);
  border: 1px dashed var(--color-border-strong);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
  transition:
    color var(--duration-fast, 150ms) ease,
    border-color var(--duration-fast, 150ms) ease;
}
.advghost:hover {
  color: var(--color-primary);
  border-color: var(--color-primary);
}

/* 保存条 */
.adv__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
  padding-top: 4px;
  border-top: 1px solid var(--color-border);
}
.adv__foot-note {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: var(--text-micro);
  color: var(--color-faint);
  line-height: 1.5;
  max-width: 46ch;
}
.adv__foot-ic {
  color: var(--color-primary);
  flex-shrink: 0;
}
.advsave {
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
.advsave:hover:not(:disabled) {
  background: var(--color-primary-press);
}
.advsave:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

@media (max-width: 560px) {
  .advgrid2,
  .toggles {
    grid-template-columns: 1fr;
  }
  .rdr__row {
    grid-template-columns: 1fr auto 60px auto;
  }
  .rdr__arrow {
    display: none;
  }
}
</style>
