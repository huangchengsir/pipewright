<!--
  CodeViewer.vue — Story 7-4: 只读代码查看(Monaco 动态 import,按扩展名高亮)。

  NFR-4 体积铁律:Monaco 经 `import('monaco-editor')` **动态** 加载 → Vite 拆为
  独立 chunk,不进主 bundle、不抬首屏。仅当用户首次打开某文件时才下载 Monaco。
  若 Monaco 加载失败(网络/体积异常)→ 退化为 <pre> + 行号纯文本视图(仍可读)。

  状态:
    · idle      — 未选文件(提示从左侧选择)。
    · loading   — 正在拉 blob 或加载 Monaco。
    · ready     — Monaco 已渲染只读内容。
    · fallback  — Monaco 加载失败,纯 <pre> 行号降级(仍可读)。
    · binary    — 二进制文件,不可预览。
    · degraded  — 克隆失败/源码暂不可读(blob content 空且非空文件)。
    · error     — blob 拉取失败(网络/404)。

  纯只读:Monaco readOnly + domReadOnly;无编辑/保存。
-->
<script setup lang="ts">
import { ref, shallowRef, computed, watch, onUnmounted, nextTick } from 'vue'
import { getSourceBlob, type SourceBlob } from '../../api/source'
import { HttpError } from '../../api/http'
import { languageForPath } from './monacoLang'
import { installMonacoEnvironment } from './monacoEnv'
import { useThemeStore } from '../../stores/theme'

// Monaco 类型仅作类型标注(type-only import 不进运行时 bundle)。
import type * as MonacoNS from 'monaco-editor'

const props = defineProps<{
  projectId: string
  treeRef: string
  /** 选中文件路径;null ⇒ 未选。 */
  path: string | null
}>()

const themeStore = useThemeStore()

type ViewState =
  | 'idle'
  | 'loading'
  | 'ready'
  | 'fallback'
  | 'binary'
  | 'degraded'
  | 'error'

const viewState = ref<ViewState>('idle')
const errorMsg = ref('')
const blob = ref<SourceBlob | null>(null)

// ─── Monaco 句柄(动态加载;shallowRef 避免深度响应式代理 editor 实例) ───────
const editorHost = ref<HTMLElement | null>(null)
let monaco: typeof MonacoNS | null = null
let editor: MonacoNS.editor.IStandaloneCodeEditor | null = null
const monacoFailed = shallowRef(false)

// 防竞态:仅最后一次请求生效。
let loadToken = 0

function monacoTheme(): string {
  return themeStore.current === 'light' ? 'vs' : 'vs-dark'
}

/** 懒加载 Monaco;失败置 monacoFailed。返回模块或 null。 */
async function ensureMonaco(): Promise<typeof MonacoNS | null> {
  if (monaco) return monaco
  if (monacoFailed.value) return null
  try {
    // worker 接线须在创建 editor 前装好(幂等)。
    installMonacoEnvironment()
    // 动态 import → 独立 chunk(NFR-4):monaco 不进主 bundle,仅打开文件时按需加载。
    // 【deferred / 7-4 评审】全量 monaco 会让 vite 产出 TS/CSS/HTML/JSON 语言服务 worker
    // (ts.worker ~7MB 等),它们被 go:embed 进二进制(+~11MB,23M→36M),但只读高亮用
    // 主线程 Monarch、运行时从不加载这些 worker → 纯死重。瘦身法(editor.api + 按需
    // basic-languages/<lang> 逐语言注册,绕开聚合路径)留 7-4 评审正经做 + 浏览器验高亮不挂。
    monaco = await import('monaco-editor')
    return monaco
  } catch {
    monacoFailed.value = true
    return null
  }
}

function disposeEditor(): void {
  if (editor) {
    editor.dispose()
    editor = null
  }
}

async function renderMonaco(content: string, language: string): Promise<void> {
  const m = await ensureMonaco()
  if (!m) {
    viewState.value = 'fallback'
    return
  }
  await nextTick()
  const host = editorHost.value
  if (!host) {
    viewState.value = 'fallback'
    return
  }
  disposeEditor()
  editor = m.editor.create(host, {
    value: content,
    language,
    theme: monacoTheme(),
    readOnly: true,
    domReadOnly: true,
    automaticLayout: true,
    minimap: { enabled: false },
    scrollBeyondLastLine: false,
    fontFamily: 'JetBrains Mono, ui-monospace, Menlo, monospace',
    fontSize: 13,
    lineNumbers: 'on',
    renderWhitespace: 'none',
    wordWrap: 'off',
    contextmenu: false,
  })
  viewState.value = 'ready'
}

async function loadBlob(filePath: string): Promise<void> {
  const token = ++loadToken
  viewState.value = 'loading'
  errorMsg.value = ''
  blob.value = null
  try {
    const b = await getSourceBlob(props.projectId, { ref: props.treeRef, path: filePath })
    if (token !== loadToken) return // 过期请求,丢弃
    blob.value = b

    if (b.binary) {
      disposeEditor()
      viewState.value = 'binary'
      return
    }
    // 克隆失败降级:后端显式 degraded 标志(code-review P6)。此前靠 `content==''&&size>0`
    // 推断,但克隆失败时 size=0 → 漏判 → 显空白编辑器。改据后端 degraded 标志,真实空文件(size=0、
    // 非 degraded)仍正常显示空内容。
    if (b.degraded) {
      disposeEditor()
      viewState.value = 'degraded'
      return
    }
    await renderMonaco(b.content, languageForPath(filePath))
  } catch (err) {
    if (token !== loadToken) return
    disposeEditor()
    errorMsg.value =
      err instanceof HttpError
        ? err.status === 0
          ? '无法连接到服务器'
          : err.status === 404
            ? '文件不存在'
            : err.apiError?.message ?? `加载失败(${err.status})`
        : '加载失败'
    viewState.value = 'error'
  }
}

watch(
  () => [props.projectId, props.treeRef, props.path] as const,
  () => {
    if (!props.path) {
      disposeEditor()
      viewState.value = 'idle'
      return
    }
    void loadBlob(props.path)
  },
  { immediate: true },
)

// 主题切换 → 同步 Monaco 主题。
watch(
  () => themeStore.current,
  () => {
    if (monaco && editor) monaco.editor.setTheme(monacoTheme())
  },
)

onUnmounted(disposeEditor)

function retry(): void {
  if (props.path) void loadBlob(props.path)
}

// ─── fallback / display helpers ──────────────────────────────────────────────
const fallbackLines = computed(() => {
  const c = blob.value?.content ?? ''
  return c.length ? c.split('\n') : []
})

const fileName = computed(() => (props.path ? props.path.split('/').pop() : ''))
const fileSize = computed(() => blob.value?.size ?? 0)

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}
</script>

<template>
  <section class="code-view" aria-label="代码查看">
    <!-- header: file path + meta -->
    <header class="code-view-head">
      <div class="code-view-path mono" :title="path ?? ''">
        <svg
          v-if="path"
          width="13"
          height="13"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
          <path d="M14 2v6h6" />
        </svg>
        <span class="code-view-path-text">{{ path ?? '未选择文件' }}</span>
      </div>
      <div v-if="blob && (viewState === 'ready' || viewState === 'fallback' || viewState === 'binary')" class="code-view-meta mono">
        <span>{{ formatBytes(fileSize) }}</span>
        <span v-if="blob.truncated" class="code-view-trunc" title="文件过大,仅显示前缀部分">已截断</span>
      </div>
    </header>

    <!-- body -->
    <div class="code-view-body">
      <!-- idle -->
      <div v-if="viewState === 'idle'" class="code-view-state">
        <svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M16 18l6-6-6-6M8 6l-6 6 6 6" />
        </svg>
        <p class="code-view-state-title">从左侧选择文件查看</p>
        <p class="code-view-state-sub">只读浏览仓库源码,语法高亮,无法编辑或提交。</p>
      </div>

      <!-- loading -->
      <div v-else-if="viewState === 'loading'" class="code-view-state">
        <span class="code-view-spin" aria-hidden="true" />
        <p class="code-view-state-title mono">加载中…</p>
      </div>

      <!-- binary -->
      <div v-else-if="viewState === 'binary'" class="code-view-state">
        <svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <rect x="3" y="3" width="18" height="18" rx="2" />
          <path d="M3 9h18M9 21V9" />
        </svg>
        <p class="code-view-state-title">二进制文件,不可预览</p>
        <p class="code-view-state-sub mono">{{ fileName }} · {{ formatBytes(fileSize) }}</p>
      </div>

      <!-- degraded (源码暂不可读) -->
      <div v-else-if="viewState === 'degraded'" class="code-view-state code-view-state--degraded">
        <svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
          <path d="M12 9v4M12 17h.01" />
        </svg>
        <p class="code-view-state-title">源码暂不可读</p>
        <p class="code-view-state-sub">仓库克隆失败或当前环境无法访问。请稍后重试或检查项目仓库配置。</p>
        <button type="button" class="code-view-retry" @click="retry">↻ 重试</button>
      </div>

      <!-- error -->
      <div v-else-if="viewState === 'error'" class="code-view-state code-view-state--err">
        <svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <path d="M12 8v4M12 16h.01" />
        </svg>
        <p class="code-view-state-title">{{ errorMsg || '文件加载失败' }}</p>
        <button type="button" class="code-view-retry" @click="retry">↻ 重试</button>
      </div>

      <!-- fallback: 纯 <pre> + 行号(Monaco 加载失败时退化,仍可读) -->
      <div v-else-if="viewState === 'fallback'" class="code-fallback" role="region" aria-label="代码内容(纯文本降级)">
        <p class="code-fallback-note mono">语法高亮组件加载失败,已降级为纯文本视图。</p>
        <ol class="code-fallback-lines">
          <li v-for="(line, i) in fallbackLines" :key="i" class="code-fallback-line">
            <span class="code-fallback-ln mono" aria-hidden="true">{{ i + 1 }}</span>
            <span class="code-fallback-text mono">{{ line }}</span>
          </li>
        </ol>
      </div>

      <!-- ready: Monaco host (始终渲染容器以便 editor.create 挂载) -->
      <div
        v-show="viewState === 'ready'"
        ref="editorHost"
        class="code-monaco"
        aria-label="代码编辑器(只读)"
      />
    </div>
  </section>
</template>

<style scoped>
.code-view {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg);
  overflow: hidden;
}

.code-view-head {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 9px 14px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-card-2);
  flex-shrink: 0;
  min-height: 38px;
}

.code-view-path {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
  color: var(--color-text);
  font-size: 0.76rem;
}

.code-view-path svg {
  flex-shrink: 0;
  color: var(--color-faint);
}

.code-view-path-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  direction: rtl;
  text-align: left;
}

.code-view-meta {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
  font-size: 0.7rem;
  color: var(--color-line-num);
}

.code-view-trunc {
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border-radius: var(--rounded-sm);
  padding: 1px 7px;
  font-weight: 600;
}

.code-view-body {
  flex: 1;
  min-height: 0;
  position: relative;
  display: flex;
}

/* ─── Monaco host ─────────────────────────────────────────────────────────── */
.code-monaco {
  flex: 1;
  width: 100%;
  height: 100%;
  min-height: 0;
}

/* ─── states ──────────────────────────────────────────────────────────────── */
.code-view-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 32px 24px;
  gap: 4px;
  color: var(--color-faint);
}

.code-view-state svg {
  color: var(--color-line-num);
  margin-bottom: 8px;
}

.code-view-state-title {
  font-size: 0.88rem;
  font-weight: 600;
  color: var(--color-text);
}

.code-view-state-sub {
  font-size: 0.78rem;
  color: var(--color-faint);
  max-width: 40ch;
  line-height: 1.55;
}

.code-view-state--degraded svg {
  color: var(--color-amber);
}

.code-view-state--err svg {
  color: var(--color-red);
}

.code-view-state--err .code-view-state-title {
  color: var(--color-red);
}

.code-view-retry {
  margin-top: 14px;
  font-family: var(--font-sans);
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--color-text);
  background: var(--color-card-2);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  padding: 6px 15px;
  cursor: pointer;
  transition: border-color var(--duration-fast);
}

.code-view-retry:hover {
  border-color: var(--color-faint);
}

.code-view-retry:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.code-view-spin {
  width: 22px;
  height: 22px;
  border-radius: var(--rounded-full);
  border: 2.5px solid var(--color-border-strong);
  border-top-color: var(--color-primary);
  animation: code-spin 0.7s linear infinite;
  margin-bottom: 8px;
}

@keyframes code-spin {
  to { transform: rotate(360deg); }
}

/* ─── fallback (<pre> + line numbers) ─────────────────────────────────────── */
.code-fallback {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: auto;
  background: var(--color-term, #0b0b0d);
}

.code-fallback-note {
  flex-shrink: 0;
  position: sticky;
  top: 0;
  padding: 6px 14px;
  font-size: 0.7rem;
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border-bottom: 1px solid var(--color-border);
}

.code-fallback-lines {
  margin: 0;
  padding: 8px 0;
  list-style: none;
  min-width: max-content;
}

.code-fallback-line {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  padding: 0 16px;
  line-height: 1.55;
  white-space: pre;
}

.code-fallback-ln {
  flex-shrink: 0;
  width: 4ch;
  text-align: right;
  font-size: 0.72rem;
  color: var(--color-line-num);
  user-select: none;
  -webkit-user-select: none;
}

.code-fallback-text {
  flex: 1;
  font-size: 0.78rem;
  color: oklch(88% 0.006 270);
  white-space: pre;
}

@media (prefers-reduced-motion: reduce) {
  .code-view-spin {
    animation-duration: 1.6s;
  }
  .code-view-retry {
    transition: none;
  }
}
</style>
