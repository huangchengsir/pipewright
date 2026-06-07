<script setup lang="ts">
/*
  ContainerAiPanel.vue — 容器页 AI 助手抽屉。复用运维终端那套 AiOpsPanel(中文描述 →
  命令卡 + 确定性风险分级 + 解释)。本页无 PTY,故 insert/execute 映射为「复制命令」,
  用户可去任一服务器终端粘贴执行。
*/
import AiOpsPanel from './AiOpsPanel.vue'
import type { CommandContext } from '../../api/aiOps'
import { useToast } from '../../composables/useToast'

defineProps<{ context: CommandContext }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const toast = useToast()

// 容器/镜像/compose 导向的快捷提示(区别于终端那套通用运维提示)。
const CONTAINER_CHIPS = [
  '看所有容器的内存和 CPU 占用',
  '清理无用镜像、停止的容器和悬空卷',
  '把某容器的日志导出到文件',
  '限制某容器内存为 512M',
  '查看某容器的端口映射和挂载',
  '列出占空间最大的镜像',
]

async function copyCommand(command: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(command)
    toast.success('命令已复制', { detail: '到任一服务器终端粘贴执行' })
  } catch {
    toast.error('复制失败', { detail: '需 https/localhost' })
  }
}
</script>

<template>
  <div class="ai-scrim" @click.self="emit('close')">
    <aside class="ai-drawer" role="dialog" aria-label="容器 AI 助手">
      <header class="ai-head">
        <span class="ai-title"><span class="spark">✦</span> AI 助手</span>
        <button class="ai-close" aria-label="关闭" @click="emit('close')">✕</button>
      </header>
      <p class="ai-sub">围绕容器 / 镜像 / compose 描述意图,AI 给出 docker 命令(带风险分级)。命令复制后到服务器终端执行。</p>
      <div class="ai-body">
        <AiOpsPanel
          :context="context"
          :chips="CONTAINER_CHIPS"
          @insert="copyCommand"
          @execute="copyCommand"
          @collapse="emit('close')"
        />
      </div>
    </aside>
  </div>
</template>

<style scoped>
.ai-scrim {
  position: fixed;
  inset: 0;
  z-index: 500;
  background: oklch(0% 0 0 / 0.42);
  display: flex;
  justify-content: flex-end;
  animation: scrimIn var(--duration-fast) var(--ease-out-expo);
}
@keyframes scrimIn {
  from { opacity: 0; }
  to { opacity: 1; }
}
.ai-drawer {
  width: min(460px, 94vw);
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--color-card);
  border-left: 1px solid var(--color-border-strong);
  box-shadow: var(--shadow-modal);
  animation: drawerIn var(--duration-normal) var(--ease-out-expo);
}
@keyframes drawerIn {
  from { transform: translateX(24px); opacity: 0.4; }
  to { transform: translateX(0); opacity: 1; }
}
.ai-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 18px 10px;
}
.ai-title {
  font-size: var(--text-section);
  font-weight: 700;
  color: var(--color-text);
}
.spark {
  color: var(--color-primary);
}
.ai-close {
  width: 28px;
  height: 28px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
}
.ai-close:hover {
  color: var(--color-text);
  border-color: var(--color-text);
}
.ai-sub {
  margin: 0;
  padding: 0 18px 12px;
  font-size: var(--text-micro);
  color: var(--color-faint);
  border-bottom: 1px solid var(--color-border);
}
.ai-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
</style>
