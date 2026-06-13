export default {
  // ─── shared ──────────────────────────────────────────────────────────────
  loadingShort: '加载中…',

  // ─── AuditTimeline ───────────────────────────────────────────────────────
  audit: {
    title: '审计日志',
    sub: '谁 · 何时 · 对什么',
    treeAria: '审计时间线',
    emptyLabel: '还没有审计记录',
    emptyHint: '创建、修改、删除凭据或项目,重置 webhook 密钥,手动触发运行等敏感操作都会在此留痕,记录不可篡改。',
    loadMore: '加载更多审计记录 →',
    via: 'Web 控制台',
    actorYou: '你',
    // 操作动词
    verbCreate: '新增',
    verbUpdate: '修改',
    verbDelete: '删除',
    verbReset: '重置',
    verbAdd: '接入',
    verbTrigger: '手动触发',
    verbDefault: '操作',
    // 操作对象
    nounCredential: '凭据',
    nounWebhookSecret: 'webhook 签名密钥',
    nounProject: '项目',
    nounRun: '运行',
    // 错误
    errConnect: '无法连接到服务器,请检查后端是否运行后重试',
    errLoad: '加载审计日志失败({status})',
    errLoadRetry: '加载审计日志失败,请稍后重试',
  },

  // ─── CodeTree / CodeTreeNode ─────────────────────────────────────────────
  tree: {
    aria: '代码目录树',
    fileAria: '仓库文件树',
    title: '文件',
    refTitle: '当前 ref: {ref}',
    loadingDir: '加载目录…',
    emptyRepo: '空仓库 / 源码暂不可读',
    emptyDir: '空目录',
    errConnect: '无法连接到服务器',
    errNotFound: '路径不存在',
    errLoad: '加载失败({status})',
    errLoadGeneric: '加载失败',
  },

  // ─── CodeViewer ──────────────────────────────────────────────────────────
  code: {
    viewAria: '代码查看',
    editorAria: '代码编辑器(只读)',
    noFileSelected: '未选择文件',
    truncated: '已截断',
    truncatedTitle: '文件过大,仅显示前缀部分',
    idleTitle: '从左侧选择文件查看',
    idleSub: '只读浏览仓库源码,语法高亮,无法编辑或提交。',
    binaryTitle: '二进制文件,不可预览',
    degradedTitle: '源码暂不可读',
    degradedSub: '仓库克隆失败或当前环境无法访问。请稍后重试或检查项目仓库配置。',
    errTitle: '文件加载失败',
    fallbackRegionAria: '代码内容(纯文本降级)',
    fallbackNote: '语法高亮组件加载失败,已降级为纯文本视图。',
    errConnect: '无法连接到服务器',
    errNotFound: '文件不存在',
    errLoad: '加载失败({status})',
    errLoadGeneric: '加载失败',
  },

  // ─── ConfirmDialog ───────────────────────────────────────────────────────
  confirm: {
    cancel: '取消',
    confirm: '确认',
    typeLabelPrefix: '输入',
    typeLabelSuffix: '确认',
    typePlaceholder: '输入 {text}…',
    typeAria: '输入 {text} 确认操作',
  },

  // ─── EmptyState ──────────────────────────────────────────────────────────
  empty: {
    defaultTitle: '暂无数据',
  },

  // ─── ErrorState ──────────────────────────────────────────────────────────
  error: {
    defaultTitle: '加载失败',
    retry: '重试',
    aiUnavailableAria: 'AI 功能暂不可用',
    aiTitle: 'AI 失败诊断',
    aiTag: '暂不可用',
    aiDesc: 'LLM 提供商无响应,本次未生成诊断。运行结果与日志照常记录,核心 CI/CD 不受影响。',
    confidenceLabel: '置信度 {n}% · {level}',
    confidenceHigh: '高',
    confidenceMedium: '中',
    confidenceLow: '低',
  },

  // ─── ToastHost ───────────────────────────────────────────────────────────
  toast: {
    hostAria: '通知',
    itemAria: '{type} 通知: {title}',
    closeAria: '关闭通知: {title}',
  },
}
