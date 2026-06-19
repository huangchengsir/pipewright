<script setup lang="ts">
/*
  ReverseProxyPanel.vue — 「域名 / 自动 HTTPS」面板(R1 / FR-1..FR-5)。

  嵌在 ServerCard 的 domains tab 里,针对这一台主机:
  - 绑定表单:域名(FQDN 基本校验,本地查重) + 上游容器(可从本机容器选,无则手填) +
    上游端口(默认取容器暴露端口,可改) → POST 创建一条路由。
  - 路由卡片:三态证书(pending 申请中 / issued 已签发 / failed 失败)。
    pending → 琥珀 spinner;issued → 绿锁 + certDetail(签发机构/有效期)+ 实时 https 链接;
    failed → 红 + certDetail 人话原因 + DNS 指引(显示本机地址供加 A 记录)+ 刷新/重试。
  - 每条路由:启停开关(POST enabled)、删除(DELETE,二次确认)。
  - 空态:友好提示绑定第一个域名。

  证书状态由后端回读(Caddy),前端只展示;reload/签发是后端职责。本面板按需懒加载:
  父组件首次切到 domains tab 才挂载,挂载即拉一次路由列表。
*/
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NIcon } from 'naive-ui'
import {
  Lock,
  LockOpen,
  AlertTriangle,
  Refresh,
  Trash,
  ArrowRight,
  ExternalLink,
  World,
  Server,
  CircleX,
} from '@vicons/tabler'
import {
  listProxyRoutes,
  createProxyRoute,
  setProxyRouteEnabled,
  refreshProxyRoute,
  deleteProxyRoute,
  getCaddyStatus,
  prepareCaddyEnv,
  removeCaddyEnv,
  type ProxyRoute,
  type CaddyStatus,
} from '../../api/reverseProxy'
import type { ContainerInfo } from '../../api/containers'
import { HttpError } from '../../api/http'
import { useToast } from '../../composables/useToast'
import { useConfirm } from '../../composables/useConfirm'
import RouteAdvancedSettings from './RouteAdvancedSettings.vue'
import InstantSubdomain from './InstantSubdomain.vue'

const props = defineProps<{
  /** Target host this panel binds domains on. */
  serverId: string
  /** SSH/host address — shown as the A-record target in DNS guidance. */
  host: string
  /** Containers on this host, for the upstream selector + default-port inference. */
  containers: ContainerInfo[]
}>()

const emit = defineEmits<{
  /** Route count changed — lets the parent tab badge stay in sync. */
  (e: 'count', n: number): void
}>()

const { t } = useI18n()
const toast = useToast()
const confirm = useConfirm()

// ─── 路由列表(懒加载) ──────────────────────────────────────────────────────────
const routes = ref<ProxyRoute[]>([])
const listState = ref<'idle' | 'loading' | 'loaded' | 'error'>('idle')
const listError = ref('')

// 路由数变化时同步给父(tab 角标)。
watch(
  () => routes.value.length,
  (n) => emit('count', n),
  { immediate: true },
)

async function loadRoutes(): Promise<void> {
  listState.value = 'loading'
  listError.value = ''
  try {
    routes.value = await listProxyRoutes(props.serverId)
    listState.value = 'loaded'
  } catch (err) {
    listState.value = 'error'
    listError.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? t('reverseProxy.errLoad', { status: err.status }))
        : t('reverseProxy.errNetwork')
  }
}

onMounted(loadRoutes)

// ─── 反代环境(pipewright-caddy 容器)状态 ────────────────────────────────────────
// 反向代理本质是一台跑在「本机」上的 Caddy 容器。awareness:把这事显式摆出来;
// consent:首次拉起前要用户确认;reversibility:可随时移除。
const caddy = ref<CaddyStatus | null>(null)
const caddyRemoving = ref(false)
const caddyDeploying = ref(false)

async function loadCaddyStatus(): Promise<void> {
  try {
    caddy.value = await getCaddyStatus(props.serverId)
  } catch {
    // 状态卡是增强信息,拉取失败不阻断主流程 —— 静默降级为「未知/未部署」展示。
    caddy.value = null
  }
}

onMounted(loadCaddyStatus)

/** 反代环境首次部署的同意确认弹窗(deploy 按钮与首次绑定共用同一文案)。 */
function confirmCaddyConsent(): Promise<boolean> {
  return confirm.open({
    title: t('reverseProxy.caddy.consentTitle'),
    body: t('reverseProxy.caddy.consentBody', { host: props.host }),
    confirmLabel: t('reverseProxy.caddy.consentConfirm'),
    variant: 'primary',
  })
}

/**
 * 用户主动部署反代环境:走与首次绑定相同的同意弹窗,确认后拉起 pipewright-caddy 容器,
 * 成功即把状态卡翻到「运行中」。给用户一条显式、自助的同意路径(无需先绑域名)。
 */
async function deployEnv(): Promise<void> {
  if (caddyDeploying.value) return
  const ok = await confirmCaddyConsent()
  if (!ok) return
  caddyDeploying.value = true
  try {
    caddy.value = await prepareCaddyEnv(props.serverId)
    toast.success(t('reverseProxy.caddy.deployed'))
  } catch (err) {
    toast.error(t('reverseProxy.caddy.deployFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('reverseProxy.errReq', { status: err.status }))
          : t('reverseProxy.errNetwork'),
    })
    // 失败后回读真实状态,避免卡片停在过时态。
    await loadCaddyStatus()
  } finally {
    caddyDeploying.value = false
  }
}

/** 移除反代环境(停止并删除 pipewright-caddy 容器,证书卷保留)。 */
async function removeEnv(): Promise<void> {
  const n = caddy.value?.routeCount ?? 0
  const ok = await confirm.open({
    title: t('reverseProxy.caddy.removeTitle'),
    body:
      n > 0
        ? t('reverseProxy.caddy.removeBodyWithRoutes', { host: props.host, n })
        : t('reverseProxy.caddy.removeBody', { host: props.host }),
    confirmLabel: t('reverseProxy.caddy.removeConfirm'),
    variant: 'danger',
  })
  if (!ok) return
  caddyRemoving.value = true
  try {
    const res = await removeCaddyEnv(props.serverId)
    if (res.ok) {
      toast.success(t('reverseProxy.caddy.removed'))
      await loadCaddyStatus()
    } else {
      toast.error(t('reverseProxy.caddy.removeFail'))
    }
  } catch (err) {
    toast.error(t('reverseProxy.caddy.removeFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('reverseProxy.errReq', { status: err.status }))
          : t('reverseProxy.errNetwork'),
    })
  } finally {
    caddyRemoving.value = false
  }
}

// ─── 绑定表单 ───────────────────────────────────────────────────────────────────
const domain = ref('')
// 上游可以是「运行中的容器」(按名走共享网络转发)或「自定义地址」(host:port 直转)。
const upstreamKind = ref<'container' | 'address'>('container')
const upstreamContainer = ref('')
const upstreamAddress = ref('')
const upstreamPort = ref('')
const submitting = ref(false)

// 该主机正在运行的容器(供上游选择器);无运行容器则降级为手填。
const runningContainers = computed(() =>
  props.containers.filter((c) => c.state === 'running').map((c) => c.names),
)
const hasContainerChoices = computed(() => runningContainers.value.length > 0)

/** 从容器的 ports 串里粗取第一个容器端口,作为上游端口默认值(如 0.0.0.0:80->8080/tcp → 8080)。 */
function inferPort(containerName: string): string {
  const c = props.containers.find((x) => x.names === containerName)
  if (!c || !c.ports) return ''
  // 形如 `0.0.0.0:80->8080/tcp, :::80->8080/tcp` —— 取箭头右侧的容器端口。
  const m = c.ports.match(/->(\d+)/)
  if (m) return m[1]
  // 退而求其次:取任意端口数字。
  const any = c.ports.match(/(\d+)\/(?:tcp|udp)/)
  return any ? any[1] : ''
}

function onContainerPick(): void {
  // 选了容器且端口还空着 → 自动带出推断端口(用户仍可改)。
  if (upstreamContainer.value && !upstreamPort.value.trim()) {
    const p = inferPort(upstreamContainer.value)
    if (p) upstreamPort.value = p
  }
}

// FQDN 基本校验:至少含一个点、合法 label、不以 - 开头/结尾。
const FQDN_RE = /^(?=.{1,253}$)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i
const normalizedDomain = computed(() => domain.value.trim().toLowerCase())
const domainValid = computed(() => FQDN_RE.test(normalizedDomain.value))
const domainDuplicate = computed(
  () =>
    normalizedDomain.value.length > 0 &&
    routes.value.some((r) => r.domain.toLowerCase() === normalizedDomain.value),
)

const portNum = computed(() => Number.parseInt(upstreamPort.value.trim(), 10))
const portValid = computed(
  () => Number.isInteger(portNum.value) && portNum.value >= 1 && portNum.value <= 65535,
)

// 自定义地址校验:合法 IPv4 / FQDN / host.docker.internal(轻量,非穷尽)。
const IPV4_RE = /^(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)$/
const HOSTNAME_RE = /^(?=.{1,253}$)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$/i
const normalizedAddress = computed(() => upstreamAddress.value.trim())
const addressValid = computed(() => {
  const v = normalizedAddress.value
  if (!v) return false
  return v === 'host.docker.internal' || IPV4_RE.test(v) || HOSTNAME_RE.test(v)
})

// 当前选中的上游目标(容器名 / 地址),供提交与校验复用。
const upstreamTarget = computed(() =>
  upstreamKind.value === 'address' ? normalizedAddress.value : upstreamContainer.value.trim(),
)
const upstreamValid = computed(() =>
  upstreamKind.value === 'address'
    ? addressValid.value
    : upstreamContainer.value.trim().length > 0,
)

/** 内联提示:域名格式 / 重复 / 地址 / 端口。空串 = 无错。 */
const formHint = computed(() => {
  if (normalizedDomain.value && !domainValid.value) return t('reverseProxy.hintInvalidDomain')
  if (domainDuplicate.value) return t('reverseProxy.hintDuplicate')
  if (upstreamKind.value === 'address' && normalizedAddress.value && !addressValid.value)
    return t('reverseProxy.hintInvalidAddress')
  if (upstreamPort.value.trim() && !portValid.value) return t('reverseProxy.hintInvalidPort')
  return ''
})

const canSubmit = computed(
  () =>
    domainValid.value &&
    !domainDuplicate.value &&
    upstreamValid.value &&
    portValid.value &&
    !submitting.value,
)

async function submit(): Promise<void> {
  if (!canSubmit.value) return
  // Consent:反代环境尚未部署时,启用第一个域名会在本机拉起 pipewright-caddy 容器
  // 占用 80/443。动手前显式征求一次同意;环境已就位则直接绑定,不再重复打扰。
  if (caddy.value && caddy.value.installed === false) {
    const ok = await confirmCaddyConsent()
    if (!ok) return
  }
  submitting.value = true
  try {
    const route = await createProxyRoute({
      serverId: props.serverId,
      domain: normalizedDomain.value,
      upstreamContainer: upstreamTarget.value,
      upstreamPort: portNum.value,
      config: { upstreamKind: upstreamKind.value },
    })
    // 新路由置顶,即时可见证书申请中状态。
    routes.value = [route, ...routes.value.filter((r) => r.id !== route.id)]
    toast.success(t('reverseProxy.bound'), { detail: route.domain })
    domain.value = ''
    upstreamContainer.value = ''
    upstreamAddress.value = ''
    upstreamPort.value = ''
    // 绑定可能刚拉起了反代环境 —— 刷新状态卡。
    void loadCaddyStatus()
  } catch (err) {
    toast.error(t('reverseProxy.bindFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('reverseProxy.errReq', { status: err.status }))
          : t('reverseProxy.errNetwork'),
    })
  } finally {
    submitting.value = false
  }
}

// R3:一键分配子域名成功 → 把新路由置顶插入列表(与手动绑定一致)。
function onSubdomainAllocated(route: ProxyRoute): void {
  routes.value = [route, ...routes.value.filter((r) => r.id !== route.id)]
  // 分配子域名同样可能首次拉起反代环境 —— 同步状态卡。
  void loadCaddyStatus()
}

// ─── 每条路由的操作:启停 / 刷新(重试)/ 删除 ──────────────────────────────────────
const busy = ref<Set<string>>(new Set())
function isBusy(id: string): boolean {
  return busy.value.has(id)
}
function setBusy(id: string, on: boolean): void {
  const next = new Set(busy.value)
  if (on) next.add(id)
  else next.delete(id)
  busy.value = next
}

function replaceRoute(updated: ProxyRoute): void {
  routes.value = routes.value.map((r) => (r.id === updated.id ? updated : r))
}

async function toggleEnabled(r: ProxyRoute): Promise<void> {
  if (isBusy(r.id)) return
  setBusy(r.id, true)
  try {
    const updated = await setProxyRouteEnabled(r.id, !r.enabled)
    replaceRoute(updated)
    toast.success(
      updated.enabled ? t('reverseProxy.enabled') : t('reverseProxy.disabled'),
      { detail: updated.domain },
    )
  } catch (err) {
    toast.error(t('reverseProxy.toggleFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('reverseProxy.errReq', { status: err.status }))
          : t('reverseProxy.errNetwork'),
    })
  } finally {
    setBusy(r.id, false)
  }
}

// ─── pending 证书自动轮询 ────────────────────────────────────────────────────────
// 证书 DNS-01/HTTP-01 签发是异步的(绑定后约数十秒才就绪),后端 Create 只置 pending,
// 不会主动回探。没有这个轮询,卡片会一直停在「申请中」直到用户手点刷新 —— 体验上像卡死。
// 这里每 POLL_MS 静默重探所有 enabled 且仍 pending 的路由(不弹 toast、不置 busy),
// 全部签发完(无 pending)即自然空转;新绑定的路由下一拍自动纳入。
const POLL_MS = 5000
let pollTimer: ReturnType<typeof setInterval> | null = null
let polling = false

async function pollPendingCerts(): Promise<void> {
  if (polling) return
  const pend = routes.value.filter((r) => r.enabled && r.certStatus === 'pending')
  if (!pend.length) return
  polling = true
  try {
    await Promise.all(
      pend.map(async (r) => {
        try {
          replaceRoute(await refreshProxyRoute(r.id))
        } catch {
          /* 静默:本拍失败不打扰用户,下一拍再试 */
        }
      }),
    )
  } finally {
    polling = false
  }
}

onMounted(() => {
  pollTimer = setInterval(pollPendingCerts, POLL_MS)
})
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})

async function refresh(r: ProxyRoute): Promise<void> {
  if (isBusy(r.id)) return
  setBusy(r.id, true)
  try {
    const updated = await refreshProxyRoute(r.id)
    replaceRoute(updated)
    toast.success(t('reverseProxy.refreshing'), { detail: updated.domain })
  } catch (err) {
    toast.error(t('reverseProxy.refreshFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('reverseProxy.errReq', { status: err.status }))
          : t('reverseProxy.errNetwork'),
    })
  } finally {
    setBusy(r.id, false)
  }
}

async function removeRoute(r: ProxyRoute): Promise<void> {
  const ok = await confirm.open({
    title: t('reverseProxy.removeTitle', { domain: r.domain }),
    body: t('reverseProxy.removeBody'),
    confirmLabel: t('reverseProxy.removeConfirm'),
    variant: 'danger',
  })
  if (!ok) return
  setBusy(r.id, true)
  try {
    const res = await deleteProxyRoute(r.id)
    if (res.ok) {
      routes.value = routes.value.filter((x) => x.id !== r.id)
      toast.success(t('reverseProxy.removed'), { detail: r.domain })
      // 路由数变化 —— 刷新反代环境状态卡的 routeCount。
      void loadCaddyStatus()
    } else {
      toast.error(t('reverseProxy.removeFail'))
    }
  } catch (err) {
    toast.error(t('reverseProxy.removeFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('reverseProxy.errReq', { status: err.status }))
          : t('reverseProxy.errNetwork'),
    })
  } finally {
    setBusy(r.id, false)
  }
}

function statusLabel(s: ProxyRoute['certStatus']): string {
  if (s === 'issued') return t('reverseProxy.statusIssued')
  if (s === 'failed') return t('reverseProxy.statusFailed')
  return t('reverseProxy.statusPending')
}
</script>

<template>
  <div class="rp">
    <!-- 头部:本机公网地址(DNS A 记录目标) -->
    <div class="rp__head">
      <div class="rp__lede">
        <NIcon :size="16" class="rp__lede-ic"><Lock /></NIcon>
        <span>{{ t('reverseProxy.lede') }}</span>
      </div>
      <div class="rp__head-right">
        <InstantSubdomain
          :server-id="serverId"
          :containers="containers"
          @allocated="onSubdomainAllocated"
        />
        <div class="rp__ip" :title="t('reverseProxy.hostIpTitle')">
          <span class="rp__ip-label">{{ t('reverseProxy.hostAddress') }}</span>
          <span class="rp__ip-val mono">{{ host }}</span>
        </div>
      </div>
    </div>

    <!-- 反代环境(pipewright-caddy 容器)状态卡 —— awareness + reversibility -->
    <div
      v-if="caddy"
      class="caddy"
      :class="[
        caddy.installed
          ? caddy.running
            ? 'caddy--running'
            : 'caddy--stopped'
          : 'caddy--absent',
      ]"
    >
      <div class="caddy__ic">
        <NIcon :size="18">
          <Server v-if="caddy.installed && caddy.running" />
          <AlertTriangle v-else-if="caddy.installed" />
          <World v-else />
        </NIcon>
      </div>
      <div class="caddy__body">
        <p class="caddy__title">
          <template v-if="caddy.installed && caddy.running">
            {{ t('reverseProxy.caddy.runningTitle') }}
          </template>
          <template v-else-if="caddy.installed">
            {{ t('reverseProxy.caddy.stoppedTitle') }}
          </template>
          <template v-else>
            {{ t('reverseProxy.caddy.absentTitle') }}
          </template>
        </p>
        <p class="caddy__desc">
          <template v-if="caddy.installed && caddy.running">
            {{
              t('reverseProxy.caddy.runningDesc', {
                image: caddy.image || t('reverseProxy.caddy.unknownImage'),
                ports: caddy.ports || '80,443',
              })
            }}
          </template>
          <template v-else-if="caddy.installed">
            {{ t('reverseProxy.caddy.stoppedDesc') }}
          </template>
          <template v-else>
            {{ t('reverseProxy.caddy.absentDesc') }}
          </template>
        </p>
      </div>
      <button
        v-if="caddy.installed"
        class="caddy__remove"
        :disabled="caddyRemoving"
        @click="removeEnv"
      >
        <NIcon :size="14"><CircleX /></NIcon>
        {{ caddyRemoving ? t('reverseProxy.caddy.removing') : t('reverseProxy.caddy.remove') }}
      </button>
      <button
        v-else
        class="caddy__deploy"
        :disabled="caddyDeploying"
        @click="deployEnv"
      >
        <NIcon :size="14"><Server /></NIcon>
        {{ caddyDeploying ? t('reverseProxy.caddy.deploying') : t('reverseProxy.caddy.deploy') }}
      </button>
    </div>

    <!-- 绑定表单 -->
    <form class="rp__form" @submit.prevent="submit">
      <div class="rp__field rp__field--grow">
        <label class="rp__label">{{ t('reverseProxy.domainLabel') }}</label>
        <input
          v-model="domain"
          class="rp__in mono"
          :class="{ 'rp__in--bad': normalizedDomain.length > 0 && (!domainValid || domainDuplicate) }"
          :placeholder="t('reverseProxy.domainPlaceholder')"
          autocomplete="off"
          spellcheck="false"
        />
      </div>
      <div class="rp__field rp__field--grow">
        <div class="rp__label-row">
          <label class="rp__label">{{ t('reverseProxy.upstreamLabel') }}</label>
          <div class="rp__seg" role="group" :aria-label="t('reverseProxy.upstreamKindLabel')">
            <button
              type="button"
              class="rp__seg-btn"
              :class="{ 'rp__seg-btn--on': upstreamKind === 'container' }"
              :aria-pressed="upstreamKind === 'container'"
              @click="upstreamKind = 'container'"
            >
              {{ t('reverseProxy.modeContainer') }}
            </button>
            <button
              type="button"
              class="rp__seg-btn"
              :class="{ 'rp__seg-btn--on': upstreamKind === 'address' }"
              :aria-pressed="upstreamKind === 'address'"
              @click="upstreamKind = 'address'"
            >
              {{ t('reverseProxy.modeAddress') }}
            </button>
          </div>
        </div>
        <template v-if="upstreamKind === 'container'">
          <select
            v-if="hasContainerChoices"
            v-model="upstreamContainer"
            class="rp__in"
            @change="onContainerPick"
          >
            <option value="">{{ t('reverseProxy.upstreamPick') }}</option>
            <option v-for="name in runningContainers" :key="name" :value="name">{{ name }}</option>
          </select>
          <input
            v-else
            v-model="upstreamContainer"
            class="rp__in mono"
            :placeholder="t('reverseProxy.upstreamPlaceholder')"
            autocomplete="off"
            spellcheck="false"
          />
        </template>
        <input
          v-else
          v-model="upstreamAddress"
          class="rp__in mono"
          :class="{ 'rp__in--bad': normalizedAddress.length > 0 && !addressValid }"
          :placeholder="t('reverseProxy.addressPlaceholder')"
          autocomplete="off"
          spellcheck="false"
        />
      </div>
      <div class="rp__field rp__field--port">
        <label class="rp__label">{{ t('reverseProxy.portLabel') }}</label>
        <input
          v-model="upstreamPort"
          class="rp__in mono"
          :class="{ 'rp__in--bad': upstreamPort.trim().length > 0 && !portValid }"
          inputmode="numeric"
          placeholder="8080"
          autocomplete="off"
        />
      </div>
      <button type="submit" class="rp__submit" :disabled="!canSubmit">
        <NIcon :size="15"><Lock /></NIcon>
        {{ submitting ? t('reverseProxy.binding') : t('reverseProxy.enable') }}
      </button>
    </form>
    <p class="rp__modehint">
      {{ upstreamKind === 'address' ? t('reverseProxy.addressHint') : t('reverseProxy.upstreamKindHint') }}
    </p>
    <p v-if="formHint" class="rp__formhint">{{ formHint }}</p>

    <!-- 路由列表 -->
    <p v-if="listState === 'error'" class="rp__msg rp__msg--err">⚠ {{ listError }}</p>
    <p v-else-if="listState === 'loading' && routes.length === 0" class="rp__msg">
      {{ t('reverseProxy.loading') }}
    </p>
    <div v-else-if="routes.length === 0" class="rp__empty">
      <NIcon :size="22" class="rp__empty-ic"><World /></NIcon>
      <p>{{ t('reverseProxy.empty') }}</p>
    </div>

    <ul v-else class="rp__routes" role="list">
      <li
        v-for="r in routes"
        :key="r.id"
        class="route"
        :class="[`route--${r.certStatus}`, { 'route--busy': isBusy(r.id), 'route--off': !r.enabled }]"
      >
        <div class="route__top">
          <div class="route__lock" :class="`route__lock--${r.certStatus}`">
            <NIcon :size="18">
              <Lock v-if="r.certStatus === 'issued'" />
              <AlertTriangle v-else-if="r.certStatus === 'failed'" />
              <LockOpen v-else />
            </NIcon>
          </div>
          <div class="route__main">
            <div class="route__dom">
              <a
                v-if="r.certStatus === 'issued' && r.enabled"
                class="route__link"
                :href="`https://${r.domain}`"
                target="_blank"
                rel="noopener noreferrer"
              >
                {{ r.domain }}
                <NIcon :size="12" class="route__extlink"><ExternalLink /></NIcon>
              </a>
              <span v-else>{{ r.domain }}</span>
            </div>
            <div class="route__flow mono">
              <NIcon :size="12"><ArrowRight /></NIcon>
              <span>{{ t('reverseProxy.proxiesTo') }}</span>
              <span class="route__chip">{{ r.upstreamContainer }}:{{ r.upstreamPort }}</span>
              <span class="route__kind">
                {{
                  r.config.upstreamKind === 'address'
                    ? t('reverseProxy.modeAddress')
                    : t('reverseProxy.modeContainer')
                }}
              </span>
            </div>
            <div v-if="r.config.aliases.length > 0" class="route__aliases">
              <span class="route__aliases-label">{{ t('reverseProxy.aliasesLabel') }}</span>
              <span v-for="a in r.config.aliases" :key="a" class="route__alias mono">{{ a }}</span>
            </div>
          </div>

          <span class="route__badge" :class="`route__badge--${r.certStatus}`">
            <span v-if="r.certStatus === 'pending'" class="route__spin" aria-hidden="true" />
            {{ statusLabel(r.certStatus) }}
          </span>

          <button
            class="route__toggle"
            :class="{ 'route__toggle--on': r.enabled }"
            :disabled="isBusy(r.id)"
            :title="r.enabled ? t('reverseProxy.disableTitle') : t('reverseProxy.enableTitle')"
            :aria-label="r.enabled ? t('reverseProxy.disableTitle') : t('reverseProxy.enableTitle')"
            @click="toggleEnabled(r)"
          />

          <button
            class="route__del"
            :disabled="isBusy(r.id)"
            :title="t('reverseProxy.removeTitleShort')"
            :aria-label="t('reverseProxy.removeTitle', { domain: r.domain })"
            @click="removeRoute(r)"
          >
            <NIcon :size="15"><Trash /></NIcon>
          </button>
        </div>

        <!-- pending:申请中状态行 -->
        <p v-if="r.certStatus === 'pending'" class="route__stat route__stat--pending">
          {{ r.certDetail || t('reverseProxy.pendingDefault') }}
        </p>

        <!-- issued:证书详情卡 -->
        <div v-else-if="r.certStatus === 'issued'" class="cert">
          <div class="cert__row">
            <span class="cert__k">{{ t('reverseProxy.certDetailLabel') }}</span>
            <span class="cert__v mono">{{ r.certDetail || t('reverseProxy.certIssuedFallback') }}</span>
          </div>
          <div class="cert__row">
            <span class="cert__k">{{ t('reverseProxy.tlsLabel') }}</span>
            <span class="cert__v mono">{{ t('reverseProxy.tlsAuto') }}</span>
          </div>
        </div>

        <!-- failed:人话原因 + DNS 指引 + 刷新/重试 -->
        <template v-else>
          <p class="route__stat route__stat--failed">
            {{ r.certDetail || t('reverseProxy.failedDefault') }}
          </p>
          <div class="route__guide">
            <NIcon :size="14" class="route__guide-ic"><World /></NIcon>
            <span>{{ t('reverseProxy.dnsGuide', { host }) }}</span>
            <code class="route__guide-rec mono">A&nbsp;&nbsp;{{ r.domain }}&nbsp;&nbsp;→&nbsp;&nbsp;{{ host }}</code>
          </div>
          <div class="route__fix">
            <button class="route__retry" :disabled="isBusy(r.id)" @click="refresh(r)">
              <NIcon :size="13"><Refresh /></NIcon>
              {{ t('reverseProxy.retry') }}
            </button>
          </div>
        </template>

        <!-- R2:每条路由的「高级设置」(别名 / 访问控制 / 加固 / 重定向) -->
        <RouteAdvancedSettings :route="r" @saved="replaceRoute" />
      </li>
    </ul>
  </div>
</template>

<style scoped>
.rp {
  padding: 16px 18px 20px;
}

/* 头部:lede + 本机地址 */
.rp__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 14px;
}
.rp__lede {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--text-label);
  color: var(--color-dim);
  line-height: 1.5;
  max-width: 56ch;
}
.rp__lede-ic {
  color: var(--color-primary);
  flex-shrink: 0;
}
.rp__head-right {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.rp__ip {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 5px 11px;
  border-radius: var(--rounded-full);
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
  white-space: nowrap;
}
.rp__ip-label {
  font-size: var(--text-micro);
  color: var(--color-faint);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}
.rp__ip-val {
  font-size: var(--text-label);
  color: var(--color-text);
  font-weight: 600;
}

/* 反代环境状态卡(awareness + reversibility) */
.caddy {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  margin-bottom: 16px;
  padding: 13px 15px;
  border-radius: var(--rounded-lg);
  border: 1px solid var(--color-border);
  background: var(--color-card-2);
}
.caddy--running {
  border-color: var(--color-green-line);
  background: var(--color-green-soft);
}
.caddy--stopped {
  border-color: var(--color-amber);
  background: var(--color-amber-soft);
}
.caddy--absent {
  border-color: var(--color-border-strong);
  background: var(--color-inset);
}
.caddy__ic {
  width: 32px;
  height: 32px;
  border-radius: var(--rounded-md);
  display: grid;
  place-items: center;
  flex-shrink: 0;
  border: 1px solid var(--color-border);
  background: var(--color-card);
}
.caddy--running .caddy__ic {
  color: var(--color-green);
  border-color: var(--color-green-line);
}
.caddy--stopped .caddy__ic {
  color: var(--color-amber);
  border-color: var(--color-amber);
}
.caddy--absent .caddy__ic {
  color: var(--color-primary);
}
.caddy__body {
  flex: 1;
  min-width: 0;
}
.caddy__title {
  margin: 0;
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-text);
  line-height: 1.4;
}
.caddy__desc {
  margin: 3px 0 0;
  font-size: var(--text-micro);
  color: var(--color-dim);
  line-height: 1.6;
}
.caddy__remove {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  align-self: center;
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 6px 12px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-dim);
  cursor: pointer;
  transition:
    color var(--duration-fast, 150ms) var(--ease-out-expo, ease),
    border-color var(--duration-fast, 150ms) var(--ease-out-expo, ease),
    background var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.caddy__remove:hover:not(:disabled) {
  color: var(--color-red);
  border-color: var(--color-red-line);
  background: var(--color-red-soft);
}
.caddy__remove:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.caddy__deploy {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  align-self: center;
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 7px 14px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-primary);
  background: var(--color-primary);
  color: #fff;
  cursor: pointer;
  transition: filter var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.caddy__deploy:hover:not(:disabled) {
  background: var(--color-primary-press);
}
.caddy__deploy:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* 绑定表单 */
.rp__form {
  display: flex;
  align-items: flex-end;
  gap: 10px;
  flex-wrap: wrap;
}
.rp__field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.rp__field--grow {
  flex: 1;
  min-width: 180px;
}
.rp__field--port {
  flex: 0 0 104px;
  min-width: 104px;
}
.rp__label {
  font-size: var(--text-micro);
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--color-faint);
}
.rp__label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
/* 上游类型分段切换 */
.rp__seg {
  display: inline-flex;
  padding: 2px;
  border-radius: var(--rounded-full);
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
}
.rp__seg-btn {
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 3px 11px;
  border: none;
  border-radius: var(--rounded-full);
  background: transparent;
  color: var(--color-faint);
  cursor: pointer;
  transition:
    color var(--duration-fast, 150ms) var(--ease-out-expo, ease),
    background var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.rp__seg-btn:hover:not(.rp__seg-btn--on) {
  color: var(--color-dim);
}
.rp__seg-btn--on {
  background: var(--color-card);
  color: var(--color-primary);
  box-shadow: 0 1px 2px oklch(0% 0 0 / 0.12);
}
.rp__in {
  font-size: var(--text-label);
  padding: 9px 12px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
  color: var(--color-text);
  transition:
    border-color var(--duration-fast, 150ms) var(--ease-out-expo, ease),
    box-shadow var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.rp__in:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.rp__in--bad {
  border-color: var(--color-red-line);
}
.rp__submit {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 38px;
  padding: 0 16px;
  font-size: var(--text-label);
  font-weight: 600;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-primary);
  background: var(--color-primary);
  color: #fff;
  cursor: pointer;
  transition: filter var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.rp__submit:hover:not(:disabled) {
  background: var(--color-primary-press);
}
.rp__submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.rp__modehint {
  margin: 9px 0 0;
  font-size: var(--text-micro);
  color: var(--color-faint);
  line-height: 1.6;
}
.rp__formhint {
  margin: 9px 0 0;
  font-size: var(--text-micro);
  color: var(--color-red);
}

/* 列表通用消息 / 空态 */
.rp__msg {
  margin: 18px 0 0;
  font-size: var(--text-label);
  color: var(--color-faint);
}
.rp__msg--err {
  color: var(--color-red);
}
.rp__empty {
  margin-top: 18px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  text-align: center;
  padding: 30px 24px;
  border: 1px dashed var(--color-border-strong);
  border-radius: var(--rounded-xl);
  background: var(--color-inset);
}
.rp__empty-ic {
  color: var(--color-faint);
}
.rp__empty p {
  margin: 0;
  font-size: var(--text-label);
  color: var(--color-dim);
  max-width: 44ch;
  line-height: 1.6;
}

/* 路由卡 */
.rp__routes {
  list-style: none;
  margin: 18px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.route {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-xl);
  background: var(--color-card-2);
  overflow: hidden;
  transition:
    border-color var(--duration-fast, 150ms) var(--ease-out-expo, ease),
    box-shadow var(--duration-normal, 300ms) var(--ease-out-expo, ease);
}
.route--issued {
  border-color: var(--color-green-line);
  box-shadow: 0 0 0 1px var(--color-green-line);
}
.route--failed {
  border-color: var(--color-red-line);
}
.route--busy {
  opacity: 0.6;
}
.route--off {
  opacity: 0.72;
}
.route__top {
  display: flex;
  align-items: center;
  gap: 13px;
  padding: 13px 15px;
}
.route__lock {
  width: 36px;
  height: 36px;
  border-radius: var(--rounded-lg);
  display: grid;
  place-items: center;
  flex-shrink: 0;
  border: 1px solid var(--color-border);
  color: var(--color-amber);
  background: var(--color-amber-soft);
}
.route__lock--issued {
  color: var(--color-green);
  background: var(--color-green-soft);
  border-color: var(--color-green-line);
}
.route__lock--failed {
  color: var(--color-red);
  background: var(--color-red-soft);
  border-color: var(--color-red-line);
}
.route__main {
  flex: 1;
  min-width: 0;
}
.route__dom {
  font-size: var(--text-body);
  font-weight: 600;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.route__link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--color-green);
  text-decoration: none;
}
.route__link:hover {
  text-decoration: underline;
}
.route__extlink {
  opacity: 0.7;
}
.route__flow {
  margin-top: 4px;
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: var(--text-micro);
  color: var(--color-faint);
  min-width: 0;
}
.route__chip {
  padding: 2px 7px;
  border-radius: var(--rounded-sm);
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  color: var(--color-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.route__kind {
  flex-shrink: 0;
  padding: 1px 7px;
  border-radius: var(--rounded-full);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  font-size: var(--text-micro);
  white-space: nowrap;
}
.route__aliases {
  margin-top: 5px;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}
.route__aliases-label {
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.route__alias {
  font-size: var(--text-micro);
  padding: 1px 7px;
  border-radius: var(--rounded-full);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.route__badge {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  flex-shrink: 0;
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 5px 10px;
  border-radius: var(--rounded-full);
  white-space: nowrap;
}
.route__badge--pending {
  color: var(--color-amber);
  background: var(--color-amber-soft);
}
.route__badge--issued {
  color: var(--color-green);
  background: var(--color-green-soft);
}
.route__badge--failed {
  color: var(--color-red);
  background: var(--color-red-soft);
}
.route__spin {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  border: 2px solid currentColor;
  border-right-color: transparent;
  animation: rpSpin 0.7s linear infinite;
}
@keyframes rpSpin {
  to {
    transform: rotate(360deg);
  }
}

/* 启停开关 */
.route__toggle {
  width: 42px;
  height: 24px;
  border-radius: var(--rounded-full);
  background: var(--color-border-strong);
  border: 1px solid var(--color-border);
  position: relative;
  cursor: pointer;
  flex-shrink: 0;
  transition: background var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.route__toggle::after {
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
.route__toggle--on {
  background: var(--color-primary);
  border-color: transparent;
}
.route__toggle--on::after {
  left: 22px;
}
.route__toggle:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.route__del {
  display: grid;
  place-items: center;
  flex-shrink: 0;
  padding: 7px;
  border-radius: var(--rounded-md);
  border: none;
  background: transparent;
  color: var(--color-faint);
  cursor: pointer;
  transition:
    color var(--duration-fast, 150ms) var(--ease-out-expo, ease),
    background var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.route__del:hover:not(:disabled) {
  color: var(--color-red);
  background: var(--color-red-soft);
}
.route__del:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* 状态行 */
.route__stat {
  margin: 0;
  padding: 0 15px 13px;
  font-size: var(--text-micro);
}
.route__stat--pending {
  color: var(--color-amber);
}
.route__stat--failed {
  color: var(--color-red);
}

/* 证书详情卡 */
.cert {
  margin: 0 15px;
  padding: 12px 0 14px;
  border-top: 1px dashed var(--color-border-strong);
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px 22px;
}
.cert__row {
  display: flex;
  gap: 9px;
  font-size: var(--text-micro);
  min-width: 0;
}
.cert__k {
  color: var(--color-faint);
  min-width: 58px;
  flex-shrink: 0;
}
.cert__v {
  color: var(--color-dim);
  min-width: 0;
  word-break: break-word;
}

/* DNS 指引 */
.route__guide {
  margin: 0 15px;
  padding: 11px 13px;
  display: flex;
  align-items: center;
  gap: 9px;
  flex-wrap: wrap;
  border-radius: var(--rounded-md);
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  font-size: var(--text-micro);
  color: var(--color-dim);
  line-height: 1.6;
}
.route__guide-ic {
  color: var(--color-primary);
  flex-shrink: 0;
}
.route__guide-rec {
  padding: 3px 8px;
  border-radius: var(--rounded-sm);
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  color: var(--color-text);
  white-space: nowrap;
}
.route__fix {
  padding: 11px 15px 14px;
  display: flex;
  gap: 10px;
  align-items: center;
}
.route__retry {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 6px 12px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-dim);
  cursor: pointer;
  transition:
    color var(--duration-fast, 150ms) var(--ease-out-expo, ease),
    border-color var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.route__retry:hover:not(:disabled) {
  color: var(--color-primary);
  border-color: var(--color-primary);
}
.route__retry:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
