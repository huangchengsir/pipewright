<!--
  CodeTree.vue — Story 7-4: 只读代码目录树(懒展开)。

  · 根目录在挂载/ref 变化时拉一次;点目录懒展开(首次展开拉子树并缓存)。
  · 点文件 emit('select', entry) 给父级加载 blob。
  · 纯只读:不构造路径,entry.path 由后端规范化后原样回传。
  · degraded(克隆失败)由父级 ProjectCode 统一接管(本组件只在根树报错/空时
    上报,父级决定整页友好态)。
  · 递归渲染下沉到 CodeTreeNode;目录状态集中在本组件,经 provide 共享。
-->
<script setup lang="ts">
import { ref, reactive, watch, onMounted, provide } from 'vue'
import { useI18n } from 'vue-i18n'
import { getSourceTree, type SourceEntry } from '../../api/source'
import { HttpError } from '../../api/http'
import CodeTreeNode from './CodeTreeNode.vue'
import {
  CODE_TREE_CTX,
  type CodeTreeCtx,
  type DirState,
} from './codeTreeContext'

const props = defineProps<{
  projectId: string
  treeRef: string
  /** 当前选中文件路径(高亮用)。 */
  selectedPath: string | null
}>()

const emit = defineEmits<{
  select: [entry: SourceEntry]
  /** 根树拉取结果上报父级(用于整页 degraded 判定)。 */
  rootLoaded: [payload: { degraded: boolean; degradedReason: string; empty: boolean }]
  rootError: [message: string]
}>()

const { t } = useI18n()

// ─── per-directory node state(懒展开缓存;key = 目录 path,"" 为根) ──────────
const dirs = reactive(new Map<string, DirState>())
const expanded = reactive(new Set<string>())

const rootLoading = ref(true)
const rootError = ref('')

function ensureDir(path: string): DirState {
  let d = dirs.get(path)
  if (!d) {
    d = { loaded: false, loading: false, error: '', entries: [] }
    dirs.set(path, d)
  }
  return d
}

async function loadDir(path: string): Promise<void> {
  const d = ensureDir(path)
  if (d.loading) return
  d.loading = true
  d.error = ''
  try {
    const tree = await getSourceTree(props.projectId, { ref: props.treeRef, path })
    d.entries = tree.entries ?? []
    d.loaded = true
    if (path === '') {
      emit('rootLoaded', {
        degraded: tree.degraded === true,
        degradedReason: tree.degradedReason ?? '',
        empty: d.entries.length === 0,
      })
    }
  } catch (err) {
    const msg =
      err instanceof HttpError
        ? err.status === 0
          ? t('misc.tree.errConnect')
          : err.status === 404
            ? t('misc.tree.errNotFound')
            : err.apiError?.message ?? t('misc.tree.errLoad', { status: err.status })
        : t('misc.tree.errLoadGeneric')
    d.error = msg
    if (path === '') {
      rootError.value = msg
      emit('rootError', msg)
    }
  } finally {
    d.loading = false
    if (path === '') rootLoading.value = false
  }
}

function toggleDir(entry: SourceEntry): void {
  if (expanded.has(entry.path)) {
    expanded.delete(entry.path)
    return
  }
  expanded.add(entry.path)
  const d = ensureDir(entry.path)
  if (!d.loaded && !d.loading) void loadDir(entry.path)
}

function onEntryClick(entry: SourceEntry): void {
  if (entry.type === 'dir') {
    toggleDir(entry)
  } else {
    emit('select', entry)
  }
}

// Provide shared context to recursive nodes.
const ctx: CodeTreeCtx = {
  dirs,
  expanded,
  getDir: ensureDir,
  isExpanded: (path) => expanded.has(path),
  onEntryClick,
  selectedPath: () => props.selectedPath,
}
provide(CODE_TREE_CTX, ctx)

function reset(): void {
  dirs.clear()
  expanded.clear()
  rootError.value = ''
  rootLoading.value = true
  void loadDir('')
}

onMounted(reset)
watch(
  () => [props.projectId, props.treeRef] as const,
  () => reset(),
)

function retryRoot(): void {
  reset()
}

// 根目录状态(模板渲染根 entries)。
function rootDir(): DirState {
  return ensureDir('')
}
</script>

<template>
  <nav class="code-tree" :aria-label="t('misc.tree.aria')">
    <div class="code-tree-head">
      <span class="code-tree-title">{{ t('misc.tree.title') }}</span>
      <span v-if="treeRef" class="code-tree-ref mono" :title="t('misc.tree.refTitle', { ref: treeRef })">{{ treeRef }}</span>
    </div>

    <div class="code-tree-body" role="tree" :aria-label="t('misc.tree.fileAria')">
      <!-- root loading -->
      <div v-if="rootLoading" class="code-tree-state mono">{{ t('misc.tree.loadingDir') }}</div>

      <!-- root error (网络/服务错误;degraded 由父级整页接管) -->
      <div v-else-if="rootError" class="code-tree-state code-tree-state--err">
        <span class="mono">{{ rootError }}</span>
        <button type="button" class="code-tree-retry" @click="retryRoot">↻ {{ t('misc.error.retry') }}</button>
      </div>

      <!-- empty root -->
      <div
        v-else-if="rootDir().loaded && rootDir().entries.length === 0"
        class="code-tree-state mono"
      >
        {{ t('misc.tree.emptyRepo') }}
      </div>

      <!-- root entries -->
      <ul v-else class="code-tree-list">
        <CodeTreeNode
          v-for="entry in rootDir().entries"
          :key="entry.path"
          :entry="entry"
          :depth="0"
        />
      </ul>
    </div>
  </nav>
</template>

<style scoped>
.code-tree {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg);
  overflow: hidden;
}

.code-tree-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 12px;
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
  background: var(--color-card-2);
}

.code-tree-title {
  font-size: 0.74rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-faint);
}

.code-tree-ref {
  margin-left: auto;
  max-width: 14ch;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.68rem;
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  border-radius: var(--rounded-full);
  padding: 1px 8px;
}

.code-tree-body {
  flex: 1;
  overflow: auto;
  padding: 6px;
  scrollbar-width: thin;
  scrollbar-color: var(--color-border-strong) transparent;
}

.code-tree-body::-webkit-scrollbar {
  width: 9px;
  height: 9px;
}

.code-tree-body::-webkit-scrollbar-thumb {
  background: var(--color-border-strong);
  border-radius: var(--rounded-full);
  border: 2px solid transparent;
  background-clip: padding-box;
}

.code-tree-list {
  margin: 0;
  padding: 0;
  list-style: none;
  min-width: max-content;
}

.code-tree-state {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
  padding: 16px 12px;
  font-size: 0.76rem;
  color: var(--color-faint);
}

.code-tree-state--err {
  color: var(--color-red);
}

.code-tree-retry {
  font-family: var(--font-sans);
  font-size: 0.74rem;
  font-weight: 500;
  color: var(--color-text);
  background: var(--color-card-2);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  padding: 4px 11px;
  cursor: pointer;
  transition: border-color var(--duration-fast);
}

.code-tree-retry:hover {
  border-color: var(--color-faint);
}

.code-tree-retry:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}
</style>
