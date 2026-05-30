/**
 * codeTreeContext — Story 7-4: CodeTree 递归节点共享上下文。
 *
 * 目录懒展开状态集中在 CodeTree.vue,经 provide/inject 下传给递归的
 * CodeTreeNode.vue,避免逐层 prop drilling。纯只读,无写。
 */
import type { InjectionKey } from 'vue'
import type { SourceEntry } from '../../api/source'

export interface DirState {
  loaded: boolean
  loading: boolean
  error: string
  entries: SourceEntry[]
}

export interface CodeTreeCtx {
  /** path → 目录状态(懒展开缓存)。 */
  dirs: Map<string, DirState>
  /** 已展开目录 path 集合。 */
  expanded: Set<string>
  /** 取(并按需创建)某目录状态。 */
  getDir: (path: string) => DirState
  isExpanded: (path: string) => boolean
  /** 点击树节点(目录 toggle / 文件 select)。 */
  onEntryClick: (entry: SourceEntry) => void
  /** 当前选中文件路径(高亮)。 */
  selectedPath: () => string | null
}

export const CODE_TREE_CTX: InjectionKey<CodeTreeCtx> = Symbol('code-tree-ctx')
