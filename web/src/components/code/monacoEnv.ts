/**
 * monacoEnv — Story 7-4: Monaco web-worker 接线(Vite ?worker)。
 *
 * 只读浏览只需 **语法高亮**(Monarch tokenizer 在主线程跑),不需要 IntelliSense /
 * 诊断 / 补全 —— 后者才依赖 TS/JSON/CSS/HTML 语言 worker。那几个 worker 体积极大
 * (ts.worker ~7MB),既抬构建内存又无意义,故只接基础 editor.worker。
 *
 * MonacoEnvironment.getWorker 对所有 label 一律返回 base editor worker:语言服务
 * 在只读场景静默退化(不报错、不影响高亮)。base worker 经 Vite `?worker` 拆为
 * 独立 chunk,仅打开文件时下载,不进主 bundle(NFR-4)。
 *
 * 该模块仅由 CodeViewer(经 ProjectCode 懒路由)引用,不影响首屏。
 */

// Vite `?worker` 虚拟模块,默认导出 Worker 构造器(类型由 vite/client 提供)。
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'

let installed = false

/** 幂等安装 MonacoEnvironment.getWorker(首次创建 editor 前调用)。 */
export function installMonacoEnvironment(): void {
  if (installed) return
  installed = true
  ;(self as unknown as { MonacoEnvironment: unknown }).MonacoEnvironment = {
    getWorker(): Worker {
      // 只读高亮场景:所有语言统一用基础 editor worker(无语言服务依赖)。
      return new EditorWorker()
    },
  }
}
