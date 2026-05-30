<script setup lang="ts">
/**
 * EnvCredsTab — 环境与凭据 tab (Story 2.4).
 * Environment list (name + target-server placeholder + env vars) + image-registry
 * binding (type + url + vault credential). Edits a local Environment[] copy and emits
 * `update`; the parent (ProjectPipeline) owns save.
 *
 * Target servers are placeholders until Story 4-1 (existence not validated yet).
 * Secret env vars / registry credentials reference a vault credentialId — plaintext is
 * never entered, stored, or shown; only the server-computed mask appears.
 */
import { ref, watch } from 'vue'
import type {
  Environment,
  BuildVar,
  RegistryType,
} from '../../api/pipelineSettings'
import type { Credential } from '../../api/credentials'

interface Props {
  environments: Environment[]
  credentials: Credential[]
  disabled?: boolean
}
const props = defineProps<Props>()

const emit = defineEmits<{
  update: [environments: Environment[]]
}>()

// ─── Local editable copy ────────────────────────────────────────────────────────

let keySeq = 0
interface VarRow extends BuildVar {
  _key: number
}
interface EnvRow {
  _key: number
  id: string
  name: string
  targetServersText: string
  envVars: VarRow[]
  registryType: RegistryType | ''
  registryUrl: string
  registryCredentialId: string
}

function toEnvRow(e: Environment): EnvRow {
  return {
    _key: keySeq++,
    id: e.id,
    name: e.name,
    targetServersText: e.targetServerIds.join(', '),
    envVars: e.envVars.map((v) => ({ ...v, _key: keySeq++ })),
    registryType: e.imageRegistry.type,
    registryUrl: e.imageRegistry.url,
    registryCredentialId: e.imageRegistry.credentialId ?? '',
  }
}

const envs = ref<EnvRow[]>(props.environments.map(toEnvRow))

watch(
  () => props.environments,
  (list) => {
    envs.value = list.map(toEnvRow)
  },
)

const REGISTRY_TYPES: Array<{ key: RegistryType; label: string }> = [
  { key: 'harbor', label: 'Harbor' },
  { key: 'acr', label: '阿里云 ACR' },
  { key: 'dockerhub', label: 'Docker Hub' },
  { key: 'custom', label: '自定义' },
]

// ─── Compose + emit on change ───────────────────────────────────────────────────

function compose(): Environment[] {
  return envs.value.map((e) => ({
    id: e.id,
    name: e.name,
    targetServerIds: e.targetServersText
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean),
    envVars: e.envVars.map(({ _key, ...v }) => {
      void _key
      return v
    }),
    imageRegistry: {
      type: e.registryType,
      url: e.registryUrl.trim(),
      credentialId: e.registryCredentialId || undefined,
    },
  }))
}

watch(envs, () => emit('update', compose()), { deep: true })

// ─── Env ops ────────────────────────────────────────────────────────────────────

function addEnv(): void {
  envs.value.push({
    _key: keySeq++,
    id: '',
    name: '',
    targetServersText: '',
    envVars: [],
    registryType: '',
    registryUrl: '',
    registryCredentialId: '',
  })
}

function removeEnv(envKey: number): void {
  envs.value = envs.value.filter((e) => e._key !== envKey)
}

function addVar(env: EnvRow, secret: boolean): void {
  env.envVars.push({
    _key: keySeq++,
    id: '',
    key: '',
    secret,
    value: secret ? undefined : '',
    credentialId: secret ? '' : undefined,
  })
}

function removeVar(env: EnvRow, rowKey: number): void {
  env.envVars = env.envVars.filter((v) => v._key !== rowKey)
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

function maskFor(row: VarRow): string {
  if (row.maskedValue) return row.maskedValue
  const c = props.credentials.find((x) => x.id === row.credentialId)
  return c ? c.maskedValue : '••••'
}
</script>

<template>
  <div class="env-root">
    <!-- ─── Environments ────────────────────────────────────────────────────── -->
    <section class="card">
      <header class="card-head">
        <span class="card-ic" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 21V9"/></svg>
        </span>
        环境
        <span class="card-sub">分支映射的落点 · 各环境有自己的服务器与变量</span>
        <button type="button" class="head-add" :disabled="disabled" @click="addEnv">+ 添加环境</button>
      </header>

      <div v-if="!envs.length" class="env-empty">
        <p>尚未定义任何环境。</p>
        <button type="button" class="addbtn" :disabled="disabled" @click="addEnv">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>
          添加第一个环境
        </button>
      </div>

      <article v-for="env in envs" :key="env._key" class="env">
        <header class="env-h">
          <input
            v-model="env.name"
            class="env-name"
            type="text"
            placeholder="环境名(如 生产 / 测试)"
            aria-label="环境名"
            :disabled="disabled"
          >
          <button
            type="button"
            class="env-del"
            aria-label="删除环境"
            :disabled="disabled"
            @click="removeEnv(env._key)"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14"/></svg>
          </button>
        </header>

        <div class="env-body">
          <!-- target servers -->
          <div class="eg">
            <div class="eg-l">
              目标服务器
              <span class="badge-soon">4-1 后启用存在性校验</span>
            </div>
            <input
              v-model="env.targetServersText"
              class="eg-input mono"
              type="text"
              placeholder="srv-prod-1, srv-prod-2(逗号分隔)"
              aria-label="目标服务器(逗号分隔)"
              :disabled="disabled"
            >
          </div>

          <!-- env vars -->
          <div class="eg">
            <div class="eg-l">环境变量</div>
            <div class="evar-list">
              <div v-for="row in env.envVars" :key="row._key" class="evar-row">
                <input v-model="row.key" class="ev-k mono" type="text" placeholder="KEY" aria-label="变量键" :disabled="disabled">
                <select
                  v-if="row.secret"
                  v-model="row.credentialId"
                  class="ev-sel"
                  aria-label="保险库凭据"
                  :disabled="disabled"
                >
                  <option value="" disabled>选择保险库凭据…</option>
                  <option v-for="c in credentials" :key="c.id" :value="c.id">{{ c.name }} · {{ c.maskedValue }}</option>
                </select>
                <input v-else v-model="row.value" class="ev-v mono" type="text" placeholder="明文值" aria-label="变量值" :disabled="disabled">
                <button
                  type="button"
                  class="vfrom"
                  :class="row.secret ? 'vfrom--vault' : 'vfrom--plain'"
                  :disabled="disabled"
                  :title="row.secret ? '点击切换为明文' : '点击切换为保险库引用'"
                  @click="toggleSecret(row)"
                >
                  <template v-if="row.secret">保险库 <span class="mask mono">{{ maskFor(row) }}</span></template>
                  <template v-else>明文</template>
                </button>
                <button type="button" class="ev-del" aria-label="删除变量" :disabled="disabled" @click="removeVar(env, row._key)">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14"/></svg>
                </button>
              </div>
              <div class="addrow">
                <button type="button" class="addbtn addbtn--sm" :disabled="disabled" @click="addVar(env, false)">+ 明文变量</button>
                <button type="button" class="addbtn addbtn--sm addbtn--vault" :disabled="disabled" @click="addVar(env, true)">+ 保险库 secret</button>
              </div>
            </div>
          </div>

          <!-- image registry -->
          <div class="eg">
            <div class="eg-l">
              镜像仓库
              <span class="eg-hint">FR-7 · 推送到外部 registry</span>
            </div>
            <div class="reg-grid">
              <select v-model="env.registryType" class="ev-sel" aria-label="镜像仓库类型" :disabled="disabled">
                <option value="">未绑定</option>
                <option v-for="r in REGISTRY_TYPES" :key="r.key" :value="r.key">{{ r.label }}</option>
              </select>
              <input
                v-model="env.registryUrl"
                class="ev-v mono"
                type="text"
                placeholder="harbor.acme.com"
                aria-label="镜像仓库地址"
                :disabled="disabled || env.registryType === ''"
              >
              <select
                v-model="env.registryCredentialId"
                class="ev-sel"
                aria-label="镜像仓库凭据"
                :disabled="disabled || env.registryType === ''"
              >
                <option value="">凭据(可选)</option>
                <option v-for="c in credentials" :key="c.id" :value="c.id">{{ c.name }} · {{ c.maskedValue }}</option>
              </select>
            </div>
          </div>
        </div>
      </article>
    </section>

    <!-- ─── Security note ───────────────────────────────────────────────────── -->
    <p class="sec-note">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true"><rect x="4" y="11" width="16" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/></svg>
      凭据在 <b>设置 · 凭据保险库</b> 统一管理,这里只做引用绑定。<b>明文绝不出现在日志或界面</b>(AC-SEC)。
    </p>
  </div>
</template>

<style scoped>
.env-root { display: flex; flex-direction: column; gap: 16px; max-width: 980px; }

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
.card-sub { margin-left: 4px; font-size: 0.73rem; color: var(--color-faint); font-weight: 400; }
.head-add {
  margin-left: auto;
  font-size: 0.78rem;
  font-weight: 500;
  color: var(--color-primary);
  background: transparent;
  border: 1px solid var(--color-border-strong);
  border-radius: 7px;
  padding: 5px 11px;
  cursor: pointer;
  font-family: var(--font-sans);
}
.head-add:hover:not(:disabled) { border-color: var(--color-primary); background: var(--color-primary-soft); }
.head-add:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.head-add:disabled { opacity: 0.5; cursor: not-allowed; }

.env-empty { padding: 40px 18px; text-align: center; display: flex; flex-direction: column; align-items: center; gap: 14px; }
.env-empty p { font-size: 0.84rem; color: var(--color-faint); }

/* ─── Single environment ─────────────────────────────────────────────────── */
.env { border-bottom: 1px solid var(--color-border); }
.env:last-of-type { border-bottom: none; }
.env-h {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 18px;
  background: var(--color-card-2);
}
.env-name {
  flex: 1;
  height: 32px;
  border: 1px solid transparent;
  background: transparent;
  color: var(--color-text);
  font-size: 0.9rem;
  font-weight: 600;
  border-radius: 7px;
  padding: 0 8px;
}
.env-name:hover:not(:disabled) { background: var(--color-inset); }
.env-name:focus { outline: none; border-color: var(--color-primary); background: var(--color-inset); box-shadow: 0 0 0 3px var(--color-primary-soft); }
.env-del {
  width: 30px; height: 30px;
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-faint);
  border-radius: 7px;
  cursor: pointer;
  display: grid; place-items: center;
  flex: none;
}
.env-del:hover:not(:disabled) { color: var(--color-red); border-color: var(--color-red-line); }
.env-del:focus-visible { outline: 2px solid var(--color-primary); }

.env-body { padding: 14px 18px; display: flex; flex-direction: column; gap: 16px; }
.eg-l {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--text-label);
  font-weight: 500;
  color: var(--color-dim);
  margin-bottom: 7px;
}
.eg-hint { font-size: 0.7rem; color: var(--color-faint); font-weight: 400; }
.badge-soon {
  font-size: 0.66rem;
  font-weight: 500;
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  border-radius: 100px;
  padding: 2px 8px;
}

.eg-input, .ev-k, .ev-v, .ev-sel {
  height: 32px;
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
  color: var(--color-text);
  border-radius: 8px;
  padding: 0 10px;
  font-size: 0.79rem;
  width: 100%;
}
.eg-input:focus, .ev-k:focus, .ev-v:focus, .ev-sel:focus { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px var(--color-primary-soft); }
.ev-sel { font-family: var(--font-sans); cursor: pointer; }
.mono { font-family: var(--font-mono); }

.evar-list { display: flex; flex-direction: column; gap: 8px; }
.evar-row { display: grid; grid-template-columns: 160px 1fr 124px 30px; gap: 8px; align-items: center; }

.vfrom {
  height: 28px;
  border-radius: 7px;
  font-size: 0.69rem;
  font-weight: 500;
  padding: 0 8px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  white-space: nowrap;
  font-family: var(--font-sans);
}
.vfrom--plain { color: var(--color-dim); background: var(--color-inset); border: 1px solid var(--color-border); }
.vfrom--vault { color: var(--color-cyan); background: var(--color-cyan-soft); border: 1px solid var(--color-cyan-line); }
.vfrom:hover:not(:disabled) { filter: brightness(1.1); }
.vfrom:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 1px; }
.vfrom .mask { letter-spacing: 0.06em; }

.ev-del {
  width: 28px; height: 28px;
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-faint);
  border-radius: 7px;
  cursor: pointer;
  display: grid; place-items: center;
}
.ev-del:hover:not(:disabled) { color: var(--color-red); border-color: var(--color-red-line); }
.ev-del:focus-visible { outline: 2px solid var(--color-primary); }

.reg-grid { display: grid; grid-template-columns: 150px 1fr 200px; gap: 8px; }
@media (max-width: 700px) { .reg-grid { grid-template-columns: 1fr; } .evar-row { grid-template-columns: 1fr; } }

.addrow { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 2px; }
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
.addbtn--sm { padding: 5px 10px; font-size: 0.75rem; }
.addbtn:hover:not(:disabled) { border-color: var(--color-primary); background: var(--color-primary-soft); }
.addbtn:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.addbtn--vault { color: var(--color-cyan); }
.addbtn--vault:hover:not(:disabled) { border-color: var(--color-cyan-line); background: var(--color-cyan-soft); }

.sec-note {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.76rem;
  color: var(--color-faint);
  line-height: 1.5;
  padding: 4px 2px;
}
.sec-note svg { color: var(--color-cyan); flex: none; }
.sec-note b { color: var(--color-dim); font-weight: 600; }
</style>
