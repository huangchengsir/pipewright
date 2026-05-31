<script setup lang="ts">
/**
 * JobTypePicker — Jenkins/云效-style task-type gallery (modal).
 *
 * Replaces the crude type dropdown: presents each task type as a card with its
 * own icon, name, and description, grouped by category. Used both when adding a
 * new node and when changing an existing node's type.
 *
 * Controlled by the parent via `open`; emits `select(type)` and `close`.
 */
import { ref, watch, nextTick } from 'vue'
import { groupedJobTypes } from './jobConfigSchema'
import { listCustomNodes, type CustomNode } from '../../api/customNodes'
import JobTypeIcon from './JobTypeIcon.vue'

const props = defineProps<{
  open: boolean
  /** Currently selected type (highlighted), if changing an existing node */
  current?: string
  /** Heading — "添加任务" vs "更换任务类型" */
  title?: string
}>()

const emit = defineEmits<{
  (e: 'select', type: string): void
  (e: 'select-custom', node: CustomNode): void
  (e: 'close'): void
}>()

const groups = groupedJobTypes()
const dialogRef = ref<HTMLElement | null>(null)
let focusedBeforeOpen: HTMLElement | null = null

// 复用库 Tier 2:挑选时加载已保存的自定义节点(选中即插入预填好 config 的 Job)。
const customNodes = ref<CustomNode[]>([])
const customLoading = ref(false)

async function loadCustomNodes(): Promise<void> {
  customLoading.value = true
  try {
    customNodes.value = await listCustomNodes()
  } catch {
    // 复用库为附加能力,加载失败不阻断挑选内建类型。
    customNodes.value = []
  } finally {
    customLoading.value = false
  }
}

function choose(type: string): void {
  emit('select', type)
}

function chooseCustom(node: CustomNode): void {
  emit('select-custom', node)
}

function onKeydown(e: KeyboardEvent): void {
  if (!props.open) return
  if (e.key === 'Escape') {
    e.preventDefault()
    emit('close')
  }
}

watch(
  () => props.open,
  async (isOpen) => {
    if (isOpen) {
      focusedBeforeOpen = document.activeElement as HTMLElement | null
      void loadCustomNodes()
      await nextTick()
      dialogRef.value?.querySelector<HTMLElement>('.type-card')?.focus()
    } else {
      focusedBeforeOpen?.focus?.()
    }
  },
)
</script>

<template>
  <Teleport to="body">
    <Transition name="jtp-overlay">
      <div
        v-if="open"
        class="jtp-overlay"
        @click.self="emit('close')"
        @keydown="onKeydown"
      >
        <div
          ref="dialogRef"
          class="jtp-dialog"
          role="dialog"
          aria-modal="true"
          aria-labelledby="jtp-title"
        >
          <header class="jtp-head">
            <h2 id="jtp-title" class="jtp-title">{{ title || '选择任务类型' }}</h2>
            <p class="jtp-sub">挑选一个任务类型,每类有自己的配置参数</p>
            <button class="jtp-close" aria-label="关闭" @click="emit('close')">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M18 6 6 18M6 6l12 12"/>
              </svg>
            </button>
          </header>

          <div class="jtp-body">
            <!-- 复用库 Tier 2:我的自定义节点(已保存的单节点,选中即预填 config) -->
            <section v-if="customLoading || customNodes.length" class="jtp-group">
              <h3 class="jtp-group-label">我的自定义节点</h3>
              <p v-if="customLoading" class="jtp-custom-hint">加载中…</p>
              <div v-else class="jtp-grid">
                <button
                  v-for="node in customNodes"
                  :key="node.id"
                  class="type-card type-card--custom"
                  @click="chooseCustom(node)"
                >
                  <JobTypeIcon :type="node.nodeType" :size="38" />
                  <span class="type-card-body">
                    <span class="type-card-name">
                      {{ node.name }}
                      <span class="type-card-badge type-card-badge--custom">自定义</span>
                    </span>
                    <span class="type-card-desc">{{ node.description || node.summary || '已保存的自定义节点' }}</span>
                    <code class="type-card-token">{{ node.nodeType }}</code>
                  </span>
                </button>
              </div>
            </section>

            <section v-for="group in groups" :key="group.id" class="jtp-group">
              <h3 class="jtp-group-label">{{ group.label }}</h3>
              <div class="jtp-grid">
                <button
                  v-for="spec in group.specs"
                  :key="spec.type"
                  class="type-card"
                  :class="{ 'type-card--current': spec.type === current }"
                  @click="choose(spec.type)"
                >
                  <JobTypeIcon :type="spec.type" :size="38" />
                  <span class="type-card-body">
                    <span class="type-card-name">
                      {{ spec.label }}
                      <span v-if="spec.type === current" class="type-card-badge">当前</span>
                    </span>
                    <span class="type-card-desc">{{ spec.description }}</span>
                    <code class="type-card-token">{{ spec.type }}</code>
                  </span>
                </button>
              </div>
            </section>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.jtp-overlay {
  position: fixed;
  inset: 0;
  z-index: 8200;
  background: rgba(8, 10, 18, 0.5);
  backdrop-filter: blur(3px);
  display: grid;
  place-items: center;
  padding: 24px;
}

.jtp-dialog {
  width: min(840px, 100%);
  max-height: min(82vh, 760px);
  display: flex;
  flex-direction: column;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-lg, 14px);
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.32);
  overflow: hidden;
}

.jtp-head {
  position: relative;
  padding: 18px 22px 14px;
  border-bottom: 1px solid var(--color-border);
}

.jtp-title {
  margin: 0;
  font-size: 1.04rem;
  font-weight: 650;
  color: var(--color-text);
}

.jtp-sub {
  margin: 3px 0 0;
  font-size: 0.8rem;
  color: var(--color-faint);
}

.jtp-close {
  position: absolute;
  top: 14px;
  right: 14px;
  width: 30px;
  height: 30px;
  display: grid;
  place-items: center;
  background: none;
  border: none;
  border-radius: var(--rounded-md);
  color: var(--color-faint);
  cursor: pointer;
  transition: color var(--duration-fast), background-color var(--duration-fast);
}
.jtp-close:hover { color: var(--color-text); background: var(--color-inset); }
.jtp-close:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }

.jtp-body {
  padding: 18px 22px 22px;
  overflow-y: auto;
}

.jtp-group + .jtp-group { margin-top: 18px; }

.jtp-group-label {
  margin: 0 0 9px;
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-faint);
}

.jtp-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(232px, 1fr));
  gap: 10px;
}

.type-card {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  text-align: left;
  padding: 13px 14px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  cursor: pointer;
  transition: border-color var(--duration-fast), background-color var(--duration-fast),
    transform var(--duration-fast), box-shadow var(--duration-fast);
}

.type-card:hover {
  border-color: var(--color-primary);
  background: var(--color-card);
  transform: translateY(-1px);
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.12);
}

.type-card:focus-visible {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

.type-card--current {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}

/* 自定义节点卡:左侧强调条,与内建类型区分 */
.type-card--custom {
  border-left: 3px solid var(--color-primary);
}

.type-card-badge--custom {
  background: var(--color-accent, var(--color-primary));
}

.jtp-custom-hint {
  margin: 0;
  font-size: 0.78rem;
  color: var(--color-faint);
}

.type-card-body {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.type-card-name {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 0.88rem;
  font-weight: 600;
  color: var(--color-text);
}

.type-card-badge {
  font-size: 0.64rem;
  font-weight: 700;
  padding: 1px 6px;
  border-radius: 7px;
  background: var(--color-primary);
  color: #fff;
}

.type-card-desc {
  font-size: 0.76rem;
  line-height: 1.4;
  color: var(--color-dim);
}

.type-card-token {
  margin-top: 2px;
  font-family: var(--font-mono);
  font-size: 0.68rem;
  color: var(--color-faint);
}

.jtp-overlay-enter-active,
.jtp-overlay-leave-active { transition: opacity var(--duration-normal); }
.jtp-overlay-enter-from,
.jtp-overlay-leave-to { opacity: 0; }
</style>
