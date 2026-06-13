<!--
  CodeTreeNode.vue — Story 7-4: CodeTree 的递归节点(单个 entry + 展开后的子树)。

  目录展开时递归渲染自身;状态从 CODE_TREE_CTX inject(不逐层透传)。
  纯展示 + 转发点击,无数据自取。
-->
<script setup lang="ts">
import { computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SourceEntry } from '../../api/source'
import { CODE_TREE_CTX } from './codeTreeContext'

const { t } = useI18n()

const props = defineProps<{
  entry: SourceEntry
  depth: number
}>()

const injected = inject(CODE_TREE_CTX)
if (!injected) throw new Error('CodeTreeNode must be used within CodeTree')
const ctx = injected

const isDir = computed(() => props.entry.type === 'dir')
const isOpen = computed(() => isDir.value && ctx.isExpanded(props.entry.path))
const dir = computed(() => ctx.getDir(props.entry.path))
const isSelected = computed(
  () => !isDir.value && ctx.selectedPath() === props.entry.path,
)

// 缩进:每层 14px,根节点 depth=0。
const indent = computed(() => `${8 + props.depth * 14}px`)

function onClick(): void {
  ctx.onEntryClick(props.entry)
}
</script>

<template>
  <li class="tree-node" role="none">
    <button
      type="button"
      class="tree-row"
      :class="{ 'tree-row--sel': isSelected, 'tree-row--dir': isDir }"
      :style="{ paddingLeft: indent }"
      role="treeitem"
      :aria-expanded="isDir ? isOpen : undefined"
      :aria-selected="isSelected"
      :title="entry.path"
      @click="onClick"
    >
      <!-- chevron (dir only) -->
      <span class="tree-chev" aria-hidden="true">
        <svg
          v-if="isDir"
          class="tree-chev-icon"
          :class="{ 'tree-chev-icon--open': isOpen }"
          width="11"
          height="11"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2.4"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M9 6l6 6-6 6" />
        </svg>
      </span>

      <!-- icon -->
      <span class="tree-icon" aria-hidden="true">
        <svg
          v-if="isDir"
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
        </svg>
        <svg
          v-else
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
          <path d="M14 2v6h6" />
        </svg>
      </span>

      <span class="tree-name">{{ entry.name }}</span>
    </button>

    <!-- children (lazy) -->
    <ul v-if="isDir && isOpen" class="tree-children" role="group">
      <li v-if="dir.loading" class="tree-state" role="none">{{ t('misc.loadingShort') }}</li>
      <li v-else-if="dir.error" class="tree-state tree-state--err" role="none">{{ dir.error }}</li>
      <li v-else-if="dir.loaded && dir.entries.length === 0" class="tree-state" role="none">{{ t('misc.tree.emptyDir') }}</li>
      <CodeTreeNode
        v-for="child in dir.entries"
        :key="child.path"
        :entry="child"
        :depth="depth + 1"
      />
    </ul>
  </li>
</template>

<style scoped>
.tree-node {
  list-style: none;
}

.tree-row {
  display: flex;
  align-items: center;
  gap: 5px;
  width: 100%;
  padding: 3px 10px 3px 8px;
  border: none;
  background: none;
  color: var(--color-dim);
  font-family: var(--font-sans);
  font-size: 0.8rem;
  text-align: left;
  cursor: pointer;
  border-radius: var(--rounded-sm);
  transition: background-color var(--duration-fast), color var(--duration-fast);
}

.tree-row:hover {
  background: var(--color-inset);
  color: var(--color-text);
}

.tree-row:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}

.tree-row--sel {
  background: var(--color-primary-soft);
  color: var(--color-primary);
  font-weight: 600;
}

.tree-row--sel:hover {
  background: var(--color-primary-soft);
}

.tree-chev {
  width: 11px;
  height: 11px;
  flex-shrink: 0;
  display: inline-grid;
  place-items: center;
  color: var(--color-line-num);
}

.tree-chev-icon {
  transition: transform var(--duration-fast) var(--ease-out-expo);
}

.tree-chev-icon--open {
  transform: rotate(90deg);
}

.tree-icon {
  flex-shrink: 0;
  display: inline-grid;
  place-items: center;
  color: var(--color-faint);
}

.tree-row--dir .tree-icon {
  color: var(--color-amber);
}

.tree-row--sel .tree-icon {
  color: var(--color-primary);
}

.tree-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono);
  font-size: 0.76rem;
}

.tree-children {
  margin: 0;
  padding: 0;
  list-style: none;
}

.tree-state {
  list-style: none;
  padding: 3px 10px 3px 34px;
  font-size: 0.72rem;
  color: var(--color-faint);
  font-family: var(--font-mono);
}

.tree-state--err {
  color: var(--color-red);
}

@media (prefers-reduced-motion: reduce) {
  .tree-chev-icon,
  .tree-row {
    transition: none;
  }
}
</style>
