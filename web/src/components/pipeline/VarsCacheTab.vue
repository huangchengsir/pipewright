<script setup lang="ts">
/**
 * VarsCacheTab — 变量与缓存 tab (Story 2.4).
 * Build variables (plain / secret-via-vault) + dependency cache. Edits a local
 * BuildConfig copy and emits `update` upward; the parent (ProjectPipeline) owns save.
 *
 * 构建模型(Dockerfile/工具链)与产物类型(镜像/JAR/dist)已下沉到画布的 build_image 节点表单
 * (job.config),执行引擎只读节点配置;此处不再渲染那两块全局设置。但仍把 model/dockerfilePath/
 * toolchain/artifactType 原值透传进 composed,保证保存/AI 应用不抹除既有值(API 契约字段保留)。
 *
 * Secret variables reference a vault credential (credentialId) — plaintext is never
 * entered, stored, or shown here; only the server-computed mask appears.
 */
import { ref, computed, watch } from 'vue'
import type {
  BuildConfig,
  BuildVar,
  BuildModel,
  ArtifactType,
} from '../../api/pipelineSettings'
import type { Credential } from '../../api/credentials'

interface Props {
  build: BuildConfig
  credentials: Credential[]
  disabled?: boolean
}
const props = defineProps<Props>()

const emit = defineEmits<{
  update: [build: BuildConfig]
}>()

// ─── Local editable copy ────────────────────────────────────────────────────────

let keySeq = 0
interface VarRow extends BuildVar {
  _key: number
}

const model = ref<BuildModel>(props.build.model)
const dockerfilePath = ref(props.build.dockerfilePath || 'Dockerfile')
const language = ref(props.build.toolchain.language)
const version = ref(props.build.toolchain.version)
const artifactType = ref<ArtifactType>(props.build.artifactType)
const vars = ref<VarRow[]>(props.build.vars.map((v) => ({ ...v, _key: keySeq++ })))
const cacheEnabled = ref(props.build.cache.enabled)
const cachePaths = ref<string>(props.build.cache.paths.join('\n'))

// Re-sync when the upstream build changes (e.g. after a save round-trip).
watch(
  () => props.build,
  (b) => {
    model.value = b.model
    dockerfilePath.value = b.dockerfilePath || 'Dockerfile'
    language.value = b.toolchain.language
    version.value = b.toolchain.version
    artifactType.value = b.artifactType
    vars.value = b.vars.map((v) => ({ ...v, _key: keySeq++ }))
    cacheEnabled.value = b.cache.enabled
    cachePaths.value = b.cache.paths.join('\n')
  },
)

// ─── Emit composed BuildConfig on any change ────────────────────────────────────

const composed = computed<BuildConfig>(() => ({
  model: model.value,
  dockerfilePath: dockerfilePath.value.trim() || 'Dockerfile',
  toolchain: { language: language.value.trim(), version: version.value.trim() },
  artifactType: artifactType.value,
  vars: vars.value.map(({ _key, ...v }) => {
    void _key
    return v
  }),
  cache: {
    enabled: cacheEnabled.value,
    paths: cachePaths.value
      .split('\n')
      .map((p) => p.trim())
      .filter(Boolean),
  },
}))

// 规范化内容键:用于比对「子组件当前内容」与「上游 props.build」是否真不同。
// 防双向绑定回环:父收到 update 后回写 :build,本组件 watch(()=>props.build) 重置本地态
// (含 keySeq++ 造新 _key)→ composed 重算 → 若无脑 emit 则父再回写 → 无限循环 → 渲染器 OOM。
// 仅当规范化内容确有差异时才 emit,回环在内容收敛后即止。
function buildKey(b: BuildConfig): string {
  return JSON.stringify({
    model: b.model,
    dockerfilePath: (b.dockerfilePath || 'Dockerfile').trim() || 'Dockerfile',
    language: (b.toolchain?.language ?? '').trim(),
    version: (b.toolchain?.version ?? '').trim(),
    artifactType: b.artifactType,
    vars: b.vars.map((v) => ({
      id: v.id ?? '',
      key: v.key,
      secret: !!v.secret,
      value: v.value ?? '',
      credentialId: v.credentialId ?? '',
    })),
    cacheEnabled: !!b.cache?.enabled,
    cachePaths: (b.cache?.paths ?? []).map((p) => p.trim()).filter(Boolean),
  })
}

watch(
  composed,
  (b) => {
    if (buildKey(b) !== buildKey(props.build)) emit('update', b)
  },
  { deep: true },
)

// ─── Variable row ops ───────────────────────────────────────────────────────────

function addVar(secret: boolean): void {
  vars.value.push({
    _key: keySeq++,
    id: '',
    key: '',
    secret,
    value: secret ? undefined : '',
    credentialId: secret ? '' : undefined,
  })
}

function removeVar(rowKey: number): void {
  vars.value = vars.value.filter((v) => v._key !== rowKey)
}

function toggleSecret(row: VarRow): void {
  row.secret = !row.secret
  if (row.secret) {
    row.value = undefined
    row.credentialId = ''
  } else {
    row.credentialId = undefined
    row.value = ''
  }
}

// Credentials suitable for secret values (any vault credential is a valid reference).
const credOptions = computed(() => props.credentials)

function maskFor(row: VarRow): string {
  if (row.maskedValue) return row.maskedValue
  const c = props.credentials.find((x) => x.id === row.credentialId)
  return c ? c.maskedValue : '••••'
}
</script>

<template>
  <div class="vars-root">
    <!-- 构建模型 / 产物类型已移到画布 build_image 节点(job.config),此处不再重复设置。 -->

    <!-- ─── Build variables ─────────────────────────────────────────────────── -->
    <section class="card">
      <header class="card-head">
        <span class="card-ic" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9"><path d="M4 7h16M4 12h10M4 17h7"/></svg>
        </span>
        构建变量
        <span class="card-sub">构建期注入 · secret 从保险库引用,绝不落日志</span>
      </header>

      <div v-if="vars.length" class="vhd" aria-hidden="true">
        <span>键</span><span>值 / 引用</span><span>来源</span><span/>
      </div>

      <div
        v-for="row in vars"
        :key="row._key"
        class="vrow"
      >
        <input
          v-model="row.key"
          class="vk mono"
          type="text"
          placeholder="KEY"
          aria-label="变量键"
          :disabled="disabled"
        >

        <template v-if="row.secret">
          <select
            v-model="row.credentialId"
            class="vsel"
            aria-label="保险库凭据"
            :disabled="disabled"
          >
            <option value="" disabled>选择保险库凭据…</option>
            <option v-for="c in credOptions" :key="c.id" :value="c.id">
              {{ c.name }} · {{ c.maskedValue }}
            </option>
          </select>
        </template>
        <input
          v-else
          v-model="row.value"
          class="vv mono"
          type="text"
          placeholder="明文值"
          aria-label="变量值"
          :disabled="disabled"
        >

        <button
          type="button"
          class="vfrom"
          :class="row.secret ? 'vfrom--vault' : 'vfrom--plain'"
          :disabled="disabled"
          :aria-label="row.secret ? '切换为明文' : '切换为保险库引用'"
          :title="row.secret ? '点击切换为明文' : '点击切换为保险库引用'"
          @click="toggleSecret(row)"
        >
          <template v-if="row.secret">保险库 <span class="mask mono">{{ maskFor(row) }}</span></template>
          <template v-else>明文</template>
        </button>

        <button
          type="button"
          class="vdel"
          aria-label="删除变量"
          :disabled="disabled"
          @click="removeVar(row._key)"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14"/></svg>
        </button>
      </div>

      <p v-if="!vars.length" class="vempty">尚无构建变量。</p>

      <div class="addrow">
        <button type="button" class="addbtn" :disabled="disabled" @click="addVar(false)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>
          添加明文变量
        </button>
        <button type="button" class="addbtn addbtn--vault" :disabled="disabled" @click="addVar(true)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true"><rect x="4" y="11" width="16" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/></svg>
          从保险库引用 secret
        </button>
      </div>
    </section>

    <!-- ─── Dependency cache ────────────────────────────────────────────────── -->
    <section class="card">
      <header class="card-head">
        <span class="card-ic" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9"><ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v6c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"/></svg>
        </span>
        依赖缓存
        <span class="card-sub">跨构建复用,加速隔离构建</span>
      </header>

      <div class="cache-toggle">
        <button
          type="button"
          class="sw"
          :class="{ 'sw--off': !cacheEnabled }"
          role="switch"
          :aria-checked="cacheEnabled"
          aria-label="启用依赖缓存"
          :disabled="disabled"
          @click="cacheEnabled = !cacheEnabled"
        ><span class="sw-knob" aria-hidden="true"/></button>
        <div class="cache-tx">
          <b>启用依赖缓存</b>
          <span>命中后跳过重复下载,显著加速构建</span>
        </div>
      </div>

      <div class="cache-paths" :class="{ 'cache-paths--off': !cacheEnabled }">
        <label class="md-label" for="cache-paths">缓存路径(每行一条)</label>
        <textarea
          id="cache-paths"
          v-model="cachePaths"
          class="md-input mono cache-area"
          rows="3"
          placeholder="node_modules&#10;.npm"
          :disabled="disabled || !cacheEnabled"
        />
      </div>
    </section>
  </div>
</template>

<style scoped>
.vars-root {
  display: flex;
  flex-direction: column;
  gap: 18px;
  align-items: stretch;
  max-width: 980px;
}

.card {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
}

.card-head {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 13px 18px;
  border-bottom: 1px solid var(--color-border);
  font-size: 0.88rem;
  font-weight: 600;
  color: var(--color-text);
}
.card-ic {
  width: 22px; height: 22px;
  border-radius: 6px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  display: grid; place-items: center;
  flex: none;
}
.card-ic svg { width: 13px; height: 13px; }
.card-sub {
  margin-left: 4px;
  font-size: 0.73rem;
  color: var(--color-faint);
  font-weight: 400;
}

.md-label {
  display: block;
  font-size: var(--text-label);
  font-weight: 500;
  color: var(--color-dim);
  margin-bottom: 5px;
}
.md-input {
  width: 100%;
  height: 34px;
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
  color: var(--color-text);
  border-radius: var(--rounded);
  padding: 0 11px;
  font-size: 0.82rem;
  font-family: var(--font-sans);
}
.md-input:focus { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px var(--color-primary-soft); }
.mono { font-family: var(--font-mono); }

/* ─── Variable table ─────────────────────────────────────────────────────── */
.vhd, .vrow {
  display: grid;
  grid-template-columns: 180px 1fr 128px 34px;
  gap: 10px;
  align-items: center;
  padding: 9px 18px;
  border-bottom: 1px solid var(--color-border);
}
.vhd {
  font-size: 0.68rem;
  color: var(--color-faint);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  background: var(--color-card-2);
  padding-top: 8px; padding-bottom: 8px;
}
.vk, .vv, .vsel {
  height: 32px;
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
  color: var(--color-text);
  border-radius: 8px;
  padding: 0 10px;
  font-size: 0.79rem;
  width: 100%;
}
.vk:focus, .vv:focus, .vsel:focus { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px var(--color-primary-soft); }
.vsel { font-family: var(--font-sans); cursor: pointer; }

.vfrom {
  height: 28px;
  border-radius: 7px;
  font-size: 0.7rem;
  font-weight: 500;
  padding: 0 9px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
  font-family: var(--font-sans);
}
.vfrom--plain { color: var(--color-dim); background: var(--color-inset); border: 1px solid var(--color-border); }
.vfrom--vault { color: var(--color-cyan); background: var(--color-cyan-soft); border: 1px solid var(--color-cyan-line); }
.vfrom:hover:not(:disabled) { filter: brightness(1.1); }
.vfrom:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 1px; }
.vfrom .mask { letter-spacing: 0.06em; }

.vdel {
  width: 30px; height: 30px;
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-faint);
  border-radius: 7px;
  cursor: pointer;
  display: grid; place-items: center;
}
.vdel:hover:not(:disabled) { color: var(--color-red); border-color: var(--color-red-line); }
.vdel:focus-visible { outline: 2px solid var(--color-primary); }

.vempty { padding: 18px; font-size: 0.8rem; color: var(--color-faint); text-align: center; }

.addrow { display: flex; gap: 8px; padding: 12px 18px; flex-wrap: wrap; }
.addbtn {
  display: inline-flex; align-items: center; gap: 6px;
  font-size: 0.79rem; font-weight: 500;
  color: var(--color-primary);
  background: transparent;
  border: 1px dashed var(--color-border-strong);
  border-radius: 8px;
  padding: 7px 12px;
  cursor: pointer;
  font-family: var(--font-sans);
}
.addbtn:hover:not(:disabled) { border-color: var(--color-primary); background: var(--color-primary-soft); }
.addbtn:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.addbtn--vault { color: var(--color-cyan); }
.addbtn--vault:hover:not(:disabled) { border-color: var(--color-cyan-line); background: var(--color-cyan-soft); }

/* ─── Cache ──────────────────────────────────────────────────────────────── */
.cache-toggle { display: flex; align-items: center; gap: 14px; padding: 14px 18px; border-bottom: 1px solid var(--color-border); }
.sw {
  width: 40px; height: 23px;
  border-radius: 100px;
  background: var(--color-primary);
  position: relative;
  cursor: pointer;
  flex: none;
  border: none;
  padding: 0;
  transition: background-color var(--duration-fast);
}
.sw-knob { position: absolute; top: 2.5px; left: 19px; width: 18px; height: 18px; border-radius: 50%; background: #fff; transition: left var(--duration-fast); }
.sw--off { background: var(--color-border-strong); }
.sw--off .sw-knob { left: 2.5px; }
.sw:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.cache-tx b { font-size: 0.83rem; font-weight: 500; }
.cache-tx span { display: block; font-size: 0.73rem; color: var(--color-faint); margin-top: 2px; }

.cache-paths { padding: 14px 18px; transition: opacity var(--duration-fast); }
.cache-paths--off { opacity: 0.5; }
.cache-area { height: auto; padding: 8px 11px; line-height: 1.6; resize: vertical; }
</style>
