export default {
  // ─── header ────────────────────────────────────────────────────
  brandTitle: 'Pipewright · 复用库',
  brandSubtitle: '自定义节点工作室 (Custom Node Studio)',
  namePlaceholder: '给节点起个名字…',
  nameAria: '节点名称',
  cancel: '取消',
  saving: '保存中…',
  saveToLibrary: '保存到复用库',

  // ─── hero ──────────────────────────────────────────────────────
  heroEyebrow: '低代码 · 一处定义,处处复用',
  heroTitlePre: '把一段可参数化的步骤,',
  heroTitleEm: '拖拽',
  heroTitlePost: '组合成可复用节点',
  heroDescPre: '左侧积木库按分组拖进中间画布、拖动卡片排序;右侧把变量「提升」成节点表面参数。底部实时编译成已有的',
  heroDescPost: '节点 —— 后端零改,实例只配提升的那几项。',

  // ─── banners ───────────────────────────────────────────────────
  loadingBanner: '载入中…',
  loadFailed: '加载失败',
  loadFailedCode: '加载失败({code})',
  saveFailed: '保存失败',
  saveFailedCode: '保存失败({code})',
  errNameRequired: '节点名称不能为空',
  errNeedCommandStep: '至少要有一个会产生命令的步骤',
  updatedToast: '已更新自定义节点「{name}」',
  createdToast: '已创建自定义节点「{name}」',

  // ─── palette ───────────────────────────────────────────────────
  paletteHintPre: '从下面把积木',
  paletteHintStrong: '拖进',
  paletteHintPost: '中间画布(或点一下追加到末尾)。',

  // ─── compose canvas ────────────────────────────────────────────
  composeTitle: '步骤组合 · 拖动卡片可重排',
  composeEmpty: '把左侧积木拖到这里开始 →',
  moveUp: '上移',
  moveDown: '下移',
  deleteStep: '删除',

  // ─── step field labels ─────────────────────────────────────────
  fieldCommandMultiline: '命令(可多行)',
  fieldInstallCommand: '安装命令',
  fieldEchoText: '回显文本',
  phEchoText: '构建开始…',
  fieldEnvKey: '变量名',
  fieldEnvValue: '值',
  fieldTargetDir: '目标目录',
  fieldPathDir: '追加到 PATH 的目录',
  fieldArtifactPath: '产物路径 (glob)',
  fieldSaveAs: '另存为',
  fieldArchiveFile: '归档文件',
  fieldExtractTo: '解到目录',
  fieldCondition: 'shell 条件(不成立则跳过后续,set -e 安全)',
  fieldCommand: '命令',
  fieldRetryCount: '次数',
  fieldDelaySecs: '间隔秒',
  fieldTimeoutSecs: '超时秒',
  fieldSleepSecs: '等待秒数',
  fieldProbeUrl: '探测 URL',
  fieldNote: '备注(编译成 # 注释,不执行)',
  fieldTestCommand: '测试命令',
  fieldReportPath: '报告路径 (JUnit)',
  fieldMinCoverage: '覆盖率门禁 %',

  // ─── right rail tabs ───────────────────────────────────────────
  tabParams: '提升参数',
  tabMeta: '节点表面',

  // ─── params tab ────────────────────────────────────────────────
  paramsHintPre: '步骤里以',
  paramsHintPost: '引用;实例复用时只配这几项。',
  paramsEmpty: '还没有提升参数。整段脚本写死也行,提升后才可在实例里改。',
  removeParamAria: '移除参数',
  newParamLabel: '新参数',
  phDisplayLabel: '显示标签',
  phDefaultValue: '默认值',
  phOptions: '逗号分隔选项,如 20, 18, 22',
  addParam: '＋ 提升一个参数',

  // ─── param type options ────────────────────────────────────────
  paramTypeText: '文本',
  paramTypeSelect: '枚举',
  paramTypeNumber: '数字',
  paramTypeToggle: '布尔',

  // ─── meta tab ──────────────────────────────────────────────────
  metaImagePre: '运行镜像(可含 ',
  metaImagePost: ')',
  metaIcon: '图标',
  metaCategory: '分类',
  phCategory: '构建与制品',
  metaSummaryPre: '一句话说明(可含 ',
  metaSummaryPost: ')',
  imgPlaceholder: '例:node:20-alpine',
  summaryPlaceholder: '例:用 npm 构建并产出 dist',
  metaHint: '分类决定它出现在「加节点」选择器的哪一组;说明 + 图标是复用者第一眼看到的卡片。',
  defaultCategory: '自定义',

  // ─── bottom: compiled output ───────────────────────────────────
  compiledTitle: '编译产出 · templated 节点 config(后端原样跑)',
  undeclaredWarn: '⚠ 步骤引用了未提升的参数:{refs}(将原样保留,实例改不动)',
  compiledComment: '# templated 自定义节点 config —— 后端 renderTemplate({open}) 后在容器内跑',
  compiledEmpty: '(还没有任何步骤)',

  // ─── bottom: instance preview ──────────────────────────────────
  previewTitle: '实例预览 · 拖进流水线后只见这些',
  unnamedNode: '未命名节点',
  customLabel: '自定义',
  previewNote: '这正是 n8n「promote 参数→实例短清单」/ Node-RED Subflow properties 的范式:复用者不必懂内部脚本,只配暴露出来的参数。',
}
