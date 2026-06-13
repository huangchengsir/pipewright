export default {
  // ─── page header ───────────────────────────────────────────────
  title: '项目',
  subtitle: '纳管的 Gitee 仓库,每个项目对应一套流水线配置与部署目标',
  newProject: '新建项目',
  retry: '重试',

  // ─── toolbar / search / filter ─────────────────────────────────
  searchFilterAria: '项目搜索与筛选',
  searchPlaceholder: '搜索项目名、仓库地址、分支…',
  searchAria: '搜索项目',
  statusFilterAria: '状态筛选',
  statusAll: '全部状态',

  // ─── list states ───────────────────────────────────────────────
  loading: '加载中',
  emptyTitle: '还没有项目',
  emptyHint: '接入第一个 Gitee 仓库,后续可配置流水线并部署到目标服务器。',
  noMatchTitle: '没有匹配的项目',
  noMatchHint: '调整搜索词或状态筛选条件后重试。',
  clearFilter: '清除筛选',
  resultCount: '{n} 个项目',
  resultCountTotal: '(共 {total} 个)',

  // ─── project card ──────────────────────────────────────────────
  runStatusAria: '运行状态:{status}',
  lastRun: '上次运行',
  noRun: '尚无运行',
  targetServers: '目标服务器',
  notBound: '未绑定',
  credential: '仓库凭据',
  credentialRefTitle: '凭据引用(非明文)',
  updatedAt: '更新于 {time}',

  // ─── card actions ──────────────────────────────────────────────
  actionRunTitle: '手动触发运行 · {name}',
  actionRunAria: '手动触发项目 {name} 的流水线运行',
  actionRenameTitle: '重命名 {name}',
  actionRenameAria: '重命名项目 {name}',
  actionCodeTitle: '代码浏览 · {name}',
  actionCodeAria: '浏览项目 {name} 的代码',
  actionPipelineTitle: '流水线配置 · {name}',
  actionPipelineAria: '配置项目 {name} 的流水线',
  actionDeleteTitle: '删除 {name}',
  actionDeleteAria: '删除项目 {name}',

  // ─── shared modal ──────────────────────────────────────────────
  closeDialog: '关闭对话框',
  cancel: '取消',
  save: '保存',
  saving: '保存中…',

  // ─── trigger modal ─────────────────────────────────────────────
  triggerDialogAria: '手动触发 · {name}',
  triggerTitle: '手动触发运行',
  triggerSub: '{name} · 指定分支并立即创建一次流水线运行',
  branch: '分支',
  branchHint: '（可选,留空用项目默认分支）',
  commit: 'Commit',
  commitHint: '（可选,留空使用分支 HEAD）',
  commitPlaceholder: '例:a3f1c2d',
  params: '参数',
  paramsHintTyped: '（按定义填写,注入流水线为环境变量）',
  paramsHintFree: '（可选,注入流水线为环境变量）',
  triggering: '触发中…',
  runNow: '立即运行',

  // ─── create modal ──────────────────────────────────────────────
  createSub: '接入 Gitee 仓库并绑定仓库凭据',
  fieldName: '项目名称',
  fieldNamePlaceholder: '例:acme-web',
  fieldRepo: '仓库地址',
  fieldCredHint: '（仅显示 Git 令牌类型，不含明文）',
  credLoading: '加载凭据中…',
  credSelect: '选择 Git 令牌凭据',
  credEmptyPre: '尚无 Git 令牌凭据,请先前往',
  credVaultLink: '凭据保险库',
  credEmptyPost: '添加。',
  fieldDefaultBranch: '默认分支',
  fieldDefaultBranchHint: '（可选,留空由测试连接自动探测）',
  testConnection: '测试连接',
  testing: '测试中…',
  testOk: '连接成功',
  testDetectedBranch: '· 默认分支 {branch}',
  creating: '创建中…',
  createSubmit: '创建项目',

  // ─── rename modal ──────────────────────────────────────────────
  renameTitle: '重命名项目',
  renameSub: '修改项目的显示名称',

  // ─── delete modal ──────────────────────────────────────────────
  deleteDialogAria: '确认删除项目',
  deleteTitle: '删除项目',
  deleteSub: '此操作不可撤销',
  deleteConfirmPre: '确定要永久删除项目',
  deleteConfirmPost: '吗?其流水线配置、运行历史及凭据引用关系将一并清理。',
  deleting: '删除中…',
  confirmDelete: '确认删除',

  // ─── error messages ────────────────────────────────────────────
  errNetwork: '无法连接到服务器,请检查后端是否运行后重试。',
  errNetworkRetry: '无法连接到服务器,请稍后重试。',
  errLoadNetwork: '无法连接到服务器,请检查后端是否运行后重试',
  errLoadStatus: '加载项目失败({status})',
  errLoadRetry: '加载项目失败,请稍后重试',
  errNameRequired: '请输入项目名称',
  errRepoRequired: '请输入仓库地址',
  errRepoFormat: '仓库地址格式不正确,请以 https:// 或 git{\'@\'} 开头',
  errCredRequired: '请选择仓库凭据',
  errRepoFirst: '请先输入仓库地址',
  errCredFirst: '请先选择仓库凭据',
  errNameEmpty: '项目名称不能为空',

  testErrCredential: '凭据错误:请检查 Gitee 访问令牌是否有效,前往凭据保险库更新。',
  testErrUnreachable: '仓库不可达:请确认仓库地址正确,且仓库存在且可访问。',
  testErrVault: '保险库未配置 master key,无法读取凭据。',
  testErrStatus: '连接测试失败({status})',
  testErrRetry: '连接测试失败,请稍后重试。',

  createErrCredField: '凭据错误:请检查访问令牌是否有效',
  createErrCredBanner: '凭据验证失败,请更换凭据或前往凭据保险库更新。',
  createErrRepoField: '仓库不可达:请确认地址正确且可访问',
  createErrRepoBanner: '仓库地址不可达,创建失败。',
  createErrVault: '保险库未配置 master key,无法保存项目。',
  createErrStatus: '创建失败({status})',
  createErrRetry: '创建失败,请稍后重试。',

  renameErrStatus: '重命名失败({status})',
  renameErrRetry: '重命名失败,请稍后重试。',

  deleteErrStatus: '删除失败({status})',
  deleteErrRetry: '删除失败,请稍后重试。',

  triggerErrNotFound: '项目不存在,请刷新后重试。',
  triggerErrStatus: '触发失败({status})',
  triggerErrRetry: '触发失败,请稍后重试。',
}
