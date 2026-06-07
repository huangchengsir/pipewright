<script setup lang="ts">
/*
  ServerCard.vue — 单台服务器的容器管理卡片。卡片内部按「容器 / 镜像」分 tab,
  tab 内容只针对这一台(Portainer 式按主机管理)。容器 tab:生命周期操作 + 日志 + 终端;
  镜像 tab:列表 + 拉取 + 删除。生命周期/镜像写操作完成后 emit('changed') 让父刷新聚合。
*/
import { ref, computed } from 'vue'
import {
  getServerImages,
  pullImage,
  removeImage,
  type ServerContainers,
  type ContainerInfo,
  type ImageInfo,
} from '../../api/containers'
import { serviceAction } from '../../api/servers'
import { HttpError } from '../../api/http'
import { useToast } from '../../composables/useToast'
import { useConfirm } from '../../composables/useConfirm'
import { NIcon } from 'naive-ui'
import { BrandDocker, AlertTriangle } from '@vicons/tabler'
import {
  stateBucket,
  stateMeta,
  shortId,
  actionsFor,
  ACTION_HINTS,
  DANGER_COPY,
  type ActionSpec,
  type StateBucket,
} from '../../lib/containerState'

const props = defineProps<{
  group: ServerContainers
  name: string
  host: string
  /** 容器状态筛选(来自页面级筛选段);'all' = 不筛。 */
  stateFilter: 'all' | StateBucket
}>()
const emit = defineEmits<{
  (e: 'changed'): void
  (e: 'logs', c: ContainerInfo): void
  (e: 'terminal', c: ContainerInfo): void
}>()

const toast = useToast()
const confirm = useConfirm()

type TabKey = 'containers' | 'images'
const tab = ref<TabKey>('containers')

// 运行时显示名:首字母大写(docker → Docker)。
const runtimeLabel = computed(() =>
  props.group.runtime ? props.group.runtime.charAt(0).toUpperCase() + props.group.runtime.slice(1) : '',
)

// ─── 容器 ─────────────────────────────────────────────────────────────────────
const busy = ref<Set<string>>(new Set())
function rowBusy(cid: string): boolean {
  for (const k of busy.value) if (k.startsWith(cid + ':')) return true
  return false
}
function isBusy(cid: string, action: string): boolean {
  return busy.value.has(`${cid}:${action}`)
}

const visibleContainers = computed(() => {
  if (props.stateFilter === 'all') return props.group.containers
  return props.group.containers.filter((c) => stateBucket(c.state) === props.stateFilter)
})

async function runAction(c: ContainerInfo, spec: ActionSpec): Promise<void> {
  if (spec.danger) {
    const copy = DANGER_COPY[spec.action]
    const ok = await confirm.open({
      title: copy?.title(c.names) ?? `对 ${c.names} 执行 ${spec.label}?`,
      body: copy?.body ?? '该操作会影响容器运行状态。',
      confirmLabel: copy?.confirmLabel ?? spec.label,
      variant: 'danger',
    })
    if (!ok) return
  }
  const key = `${c.id}:${spec.action}`
  busy.value = new Set(busy.value).add(key)
  try {
    const res = await serviceAction(props.group.serverId, { type: 'docker', target: c.names, action: spec.action })
    if (res.ok) toast.success(`${spec.label}成功`, { detail: `${c.names} · ${props.name}` })
    else toast.error(`${spec.label}失败`, { detail: res.error || '远端命令以非零状态退出' })
  } catch (err) {
    toast.error(`${spec.label}失败`, {
      detail: err instanceof HttpError ? (err.apiError?.message ?? `请求失败(${err.status})`) : '网络错误',
    })
  } finally {
    const next = new Set(busy.value)
    next.delete(key)
    busy.value = next
    setTimeout(() => emit('changed'), 600)
  }
}

// ─── 镜像(懒加载:首次切到镜像 tab 才拉) ───────────────────────────────────────
const images = ref<ImageInfo[]>([])
const imgState = ref<'idle' | 'loading' | 'loaded' | 'error'>('idle')
const imgError = ref('')
const pullRef = ref('')
const pulling = ref(false)
const busyImage = ref('')

function repoTag(img: ImageInfo): string {
  return img.repository === '<none>' ? `<none>:${img.tag}` : `${img.repository}:${img.tag}`
}

async function loadImages(): Promise<void> {
  imgState.value = 'loading'
  imgError.value = ''
  try {
    const res = await getServerImages(props.group.serverId)
    if (!res.reachable || !res.runtime) {
      imgState.value = 'error'
      imgError.value = res.error || '该服务器不可达或无容器运行时'
      return
    }
    images.value = res.images
    imgState.value = 'loaded'
  } catch (err) {
    imgState.value = 'error'
    imgError.value =
      err instanceof HttpError ? (err.apiError?.message ?? `加载镜像失败(${err.status})`) : '加载镜像失败'
  }
}

function switchTab(t: TabKey): void {
  tab.value = t
  if (t === 'images' && imgState.value === 'idle') void loadImages()
}

async function doPull(): Promise<void> {
  const ref = pullRef.value.trim()
  if (!ref || pulling.value) return
  pulling.value = true
  try {
    const res = await pullImage(props.group.serverId, ref)
    if (res.ok) {
      toast.success('镜像已拉取', { detail: ref })
      pullRef.value = ''
      void loadImages()
    } else {
      toast.error('拉取失败', { detail: res.error || '请检查镜像名与网络' })
    }
  } catch (err) {
    toast.error('拉取失败', {
      detail: err instanceof HttpError ? (err.apiError?.message ?? `请求失败(${err.status})`) : '网络错误',
    })
  } finally {
    pulling.value = false
  }
}

async function doRemoveImage(img: ImageInfo): Promise<void> {
  const tagStr = repoTag(img)
  const ok = await confirm.open({
    title: `删除镜像 ${tagStr}?`,
    body: '将执行 docker rmi 删除该镜像。若有容器正在使用会删除失败。',
    confirmLabel: '删除镜像',
    variant: 'danger',
  })
  if (!ok) return
  busyImage.value = img.id
  try {
    const res = await removeImage(props.group.serverId, img.id, false)
    if (res.ok) {
      toast.success('镜像已删除', { detail: tagStr })
      void loadImages()
    } else {
      toast.error('删除失败', { detail: res.error || '镜像可能正被容器使用' })
    }
  } catch (err) {
    toast.error('删除失败', {
      detail: err instanceof HttpError ? (err.apiError?.message ?? `请求失败(${err.status})`) : '网络错误',
    })
  } finally {
    busyImage.value = ''
  }
}
</script>

<template>
  <article class="panel">
    <header class="panel__head">
      <div class="panel__id">
        <h2 class="panel__name">{{ name }}</h2>
        <span class="panel__host mono">{{ host }}</span>
      </div>
      <div class="panel__meta">
        <span
          class="rt-badge"
          :class="{
            'rt-badge--ok': group.reachable && group.runtime,
            'rt-badge--warn': group.reachable && !group.runtime,
            'rt-badge--down': !group.reachable,
          }"
        >
          <NIcon :size="15" class="rt-badge__icon">
            <BrandDocker v-if="group.reachable && group.runtime" />
            <AlertTriangle v-else />
          </NIcon>
          {{ !group.reachable ? '不可达' : group.runtime ? runtimeLabel : '无容器运行时' }}
        </span>
        <span v-if="group.reachable && group.runtime" class="panel__count mono">
          {{ group.running }}/{{ group.total }} 运行
        </span>
      </div>
    </header>

    <!-- 不可达 / 无运行时:行内提示 -->
    <p v-if="!group.reachable" class="panel__hint panel__hint--down">⚠ {{ group.error }}</p>
    <p v-else-if="!group.runtime" class="panel__hint">{{ group.error }}</p>

    <template v-else>
      <!-- 卡片内 tab:容器 / 镜像 -->
      <div class="card-tabs" role="tablist">
        <button class="ctab" :class="{ 'ctab--active': tab === 'containers' }" role="tab" @click="switchTab('containers')">
          容器 <span class="ctab__n">{{ group.total }}</span>
        </button>
        <button class="ctab" :class="{ 'ctab--active': tab === 'images' }" role="tab" @click="switchTab('images')">
          镜像 <span v-if="imgState === 'loaded'" class="ctab__n">{{ images.length }}</span>
        </button>
      </div>

      <!-- 容器 tab -->
      <template v-if="tab === 'containers'">
        <p v-if="group.total === 0" class="panel__hint">该服务器暂无容器。</p>
        <p v-else-if="visibleContainers.length === 0" class="panel__hint">无匹配当前筛选的容器(共 {{ group.total }} 个)。</p>
        <ul v-else class="clist" role="list">
          <li v-for="c in visibleContainers" :key="c.id" class="crow" :class="{ 'crow--busy': rowBusy(c.id) }">
            <div class="crow__state">
              <span class="dot" :class="[`dot--${stateMeta(c.state).tone}`, { 'dot--pulse': stateMeta(c.state).pulse }]" aria-hidden="true" />
              <span class="crow__statelabel" :class="`txt--${stateMeta(c.state).tone}`">{{ stateMeta(c.state).label }}</span>
            </div>
            <div class="crow__main">
              <div class="crow__name" :title="c.names">{{ c.names }}</div>
              <div class="crow__sub mono" :title="`${c.image} · ${c.id}`">{{ c.image }} · {{ shortId(c.id) }}</div>
            </div>
            <div class="crow__status">
              <div class="crow__statustxt" :title="c.status">{{ c.status }}</div>
              <div v-if="c.ports" class="crow__ports mono" :title="c.ports">{{ c.ports }}</div>
            </div>
            <div class="crow__actions">
              <button
                v-for="a in actionsFor(c.state)"
                :key="a.action"
                class="op"
                :class="[`op--${a.variant}`, { 'op--busy': isBusy(c.id, a.action) }]"
                :disabled="rowBusy(c.id)"
                :title="ACTION_HINTS[a.action]"
                @click="runAction(c, a)"
              >
                {{ a.label }}
              </button>
              <button class="op op--ghost" title="查看容器日志(docker logs)" @click="emit('logs', c)">日志</button>
              <button class="op op--ghost" :disabled="rowBusy(c.id)" title="进入容器终端(docker exec -it)" @click="emit('terminal', c)">终端</button>
            </div>
          </li>
        </ul>
      </template>

      <!-- 镜像 tab -->
      <template v-else>
        <div class="img-toolbar">
          <div class="pull">
            <input v-model="pullRef" class="pull__in mono" placeholder="拉取镜像,例:nginx:latest" @keyup.enter="doPull" />
            <button class="pull__btn" :disabled="pulling || !pullRef.trim()" @click="doPull">
              {{ pulling ? '拉取中…' : '拉取' }}
            </button>
          </div>
          <span class="grow" />
          <button class="refresh" :disabled="imgState === 'loading'" @click="loadImages">↻ 刷新</button>
        </div>
        <p v-if="imgState === 'error'" class="panel__hint panel__hint--down">⚠ {{ imgError }}</p>
        <p v-else-if="imgState === 'loading' && images.length === 0" class="panel__hint">正在加载镜像…</p>
        <p v-else-if="images.length === 0" class="panel__hint">该服务器暂无镜像。</p>
        <ul v-else class="ilist" role="list">
          <li v-for="img in images" :key="img.id + repoTag(img)" class="irow" :class="{ 'irow--busy': busyImage === img.id }">
            <div class="irow__main">
              <div class="irow__tag" :title="repoTag(img)">{{ repoTag(img) }}</div>
              <div class="irow__id mono">{{ img.id }}</div>
            </div>
            <div class="irow__size mono">{{ img.size }}</div>
            <div class="irow__age">{{ img.createdSince }}</div>
            <div class="irow__act">
              <button class="op op--ghost" :disabled="busyImage === img.id" title="删除镜像(docker rmi)" @click="doRemoveImage(img)">删除</button>
            </div>
          </li>
        </ul>
      </template>
    </template>
  </article>
</template>

<style scoped>
.panel {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg, 12px);
  box-shadow: var(--shadow);
  overflow: hidden;
}
.panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-card-2);
}
.panel__id {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
}
.panel__name {
  margin: 0;
  font-size: var(--text-section);
  font-weight: 700;
  color: var(--color-text);
}
.panel__host {
  font-size: var(--text-micro);
  color: var(--color-faint);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.panel__meta {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}
.panel__count {
  font-size: var(--text-micro);
  color: var(--color-dim);
  font-variant-numeric: tabular-nums;
}
.rt-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: var(--text-label);
  font-weight: 700;
  padding: 4px 11px 4px 9px;
  border-radius: 999px;
  border: 1px solid var(--color-border-strong);
  color: var(--color-dim);
  white-space: nowrap;
}
.rt-badge__icon {
  display: inline-flex;
}
.rt-badge--ok {
  color: var(--color-green);
  background: var(--color-green-soft);
  border-color: var(--color-green-line);
}
.rt-badge--warn {
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border-color: var(--color-amber-line);
}
.rt-badge--down {
  color: var(--color-red);
  background: var(--color-red-soft);
  border-color: var(--color-red-line);
}
.panel__hint {
  margin: 0;
  padding: 16px 18px;
  font-size: var(--text-label);
  color: var(--color-faint);
}
.panel__hint--down {
  color: var(--color-red);
}

/* 卡片内 tab */
.card-tabs {
  display: flex;
  gap: 2px;
  padding: 0 12px;
  border-bottom: 1px solid var(--color-border);
}
.ctab {
  padding: 9px 14px;
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-faint);
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-out-expo);
}
.ctab:hover {
  color: var(--color-dim);
}
.ctab--active {
  color: var(--color-text);
  border-bottom-color: var(--color-primary);
}
.ctab__n {
  font-size: var(--text-micro);
  color: var(--color-faint);
  font-variant-numeric: tabular-nums;
}

/* 容器行 */
.clist {
  list-style: none;
  margin: 0;
  padding: 0;
}
.crow {
  display: grid;
  grid-template-columns: 92px minmax(0, 1.4fr) minmax(0, 1.3fr) auto;
  align-items: center;
  gap: 14px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
  transition: background var(--duration-fast) var(--ease-out-expo);
}
.crow:last-child {
  border-bottom: none;
}
.crow:hover {
  background: var(--color-card-2);
}
.crow--busy {
  opacity: 0.55;
}
.crow__state {
  display: flex;
  align-items: center;
  gap: 7px;
}
.crow__statelabel {
  font-size: var(--text-micro);
  font-weight: 600;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--color-faint);
}
.dot--green { background: var(--color-green); }
.dot--amber { background: var(--color-amber); }
.dot--cyan { background: var(--color-cyan); }
.dot--red { background: var(--color-red); }
.dot--faint { background: var(--color-faint); }
.dot--pulse { animation: dotPulse 1.8s var(--ease-out-expo) infinite; }
@keyframes dotPulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.45; transform: scale(0.8); }
}
.txt--green { color: var(--color-green); }
.txt--amber { color: var(--color-amber); }
.txt--cyan { color: var(--color-cyan); }
.txt--red { color: var(--color-red); }
.txt--faint { color: var(--color-faint); }
.crow__main { min-width: 0; }
.crow__name {
  font-size: var(--text-body);
  font-weight: 600;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.crow__sub {
  font-size: var(--text-micro);
  color: var(--color-faint);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.crow__status { min-width: 0; }
.crow__statustxt {
  font-size: var(--text-label);
  color: var(--color-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.crow__ports {
  font-size: var(--text-micro);
  color: var(--color-faint);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.crow__actions {
  display: flex;
  align-items: center;
  gap: 6px;
  justify-content: flex-end;
  flex-wrap: wrap;
}

/* 镜像 tab */
.img-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
  flex-wrap: wrap;
}
.pull {
  display: inline-flex;
  gap: 6px;
}
.pull__in {
  width: 260px;
  max-width: 50vw;
  font-size: var(--text-label);
  padding: 6px 10px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
  color: var(--color-text);
}
.pull__in:focus {
  outline: none;
  border-color: var(--color-primary);
}
.pull__btn {
  font-size: var(--text-label);
  font-weight: 600;
  padding: 6px 14px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-primary);
  background: var(--color-primary);
  color: #fff;
  cursor: pointer;
}
.pull__btn:hover:not(:disabled) { background: var(--color-primary-press); }
.refresh {
  font-size: var(--text-label);
  font-weight: 600;
  padding: 6px 14px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
}
.refresh:hover:not(:disabled) { color: var(--color-text); border-color: var(--color-text); }
.pull__btn:disabled, .refresh:disabled { opacity: 0.5; cursor: not-allowed; }
.grow { flex: 1; }

.ilist {
  list-style: none;
  margin: 0;
  padding: 0;
}
.irow {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 100px 130px auto;
  align-items: center;
  gap: 14px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
  transition: background var(--duration-fast) var(--ease-out-expo);
}
.irow:last-child { border-bottom: none; }
.irow:hover { background: var(--color-card-2); }
.irow--busy { opacity: 0.55; }
.irow__main { min-width: 0; }
.irow__tag {
  font-size: var(--text-body);
  font-weight: 600;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.irow__id { font-size: var(--text-micro); color: var(--color-faint); }
.irow__size { font-size: var(--text-label); color: var(--color-dim); font-variant-numeric: tabular-nums; }
.irow__age { font-size: var(--text-label); color: var(--color-faint); }
.irow__act { display: flex; justify-content: flex-end; }

/* 行内迷你操作按钮 */
.op {
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 4px 10px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out-expo);
}
.op:hover:not(:disabled) { color: var(--color-text); border-color: var(--color-text); }
.op--default { color: var(--color-primary); border-color: var(--color-primary-soft); }
.op--default:hover:not(:disabled) { color: #fff; background: var(--color-primary); border-color: var(--color-primary); }
.op:disabled { opacity: 0.45; cursor: not-allowed; }
.op--busy { opacity: 0.6; pointer-events: none; }

@media (max-width: 720px) {
  .crow, .irow { grid-template-columns: 1fr; gap: 8px; }
  .crow__actions { justify-content: flex-start; }
}
</style>
