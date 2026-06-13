export default {
  // ─── 顶栏 / 面包屑 ───────────────────────────────────────────────
  breadcrumbAria: '面包屑导航',
  breadcrumbProjects: '项目',
  title: '流水线配置',

  // ─── 标签页 ─────────────────────────────────────────────────────
  tabCanvas: '流水线编排',
  tabVars: '变量与缓存',
  tabTriggers: '触发设置',
  tabEnvs: '环境与凭据',
  tabStripAria: '流水线配置标签页',

  // ─── 流水线即代码(GitOps · FR-8-12)──────────────────────────────
  pacTitle: '流水线即代码',
  pacOnHint: '运行将按运行分支读取仓库 .pipewright.yml 驱动(文件缺失或非法时回退到此处配置)。',
  pacOffHint: '开启后,每次运行按运行分支读取仓库根 .pipewright.yml 驱动(缺失或非法则回退此处配置)。',
  pacToggleFailed: '切换失败,请重试。',

  // ─── 工具栏按钮 ─────────────────────────────────────────────────
  aiGenerate: 'AI 生成流水线',
  importYaml: '从 YAML 导入',
  templates: '模板',
  validate: '校验配置',
  closeValidationPanel: '关闭校验面板',
  badgeReady: '就绪',
  badgeErrors: '{n} 错误',
  saving: '保存中…',
  saveDraft: '保存草稿',

  // ─── 横幅 / 状态 ────────────────────────────────────────────────
  dismiss: '关闭提示',
  draftSaved: '流水线草稿已保存',
  retry: '重试',
  loading: '加载中',

  // ─── 加载错误 ───────────────────────────────────────────────────
  errNoServer: '无法连接到服务器,请检查后端是否运行后重试。',
  errProjectNotFound: '项目不存在,请确认项目 ID 正确。',
  errLoadFailedStatus: '加载流水线失败({status})',
  errLoadFailedRetry: '加载流水线失败,请稍后重试。',

  // ─── 保存错误 ───────────────────────────────────────────────────
  errSaveFailedRetry: '保存失败,请稍后重试。',
  errSaveFailedStatus: '保存失败({status})',
  errInvalidStage: '阶段名不能为空或 kind 不在允许值内,请检查后重试。',
  errInvalidJob: '任务名称或类型不能为空,请补充后重试。',
  errDuplicateId: '阶段或任务 ID 重复,请删除重复项后重试。',
  errInvalidBuild: '构建模型须为 dockerfile/toolchain,产物类型须为 image/jar/dist。',
  errInvalidVar: '变量键不能为空且同作用域内不可重复;secret 变量须选择保险库凭据。',
  errInvalidEnvironment: '环境名不能为空,镜像仓库类型须为 harbor/acr/dockerhub/custom。',
  errCredentialNotFound: '引用的保险库凭据不存在,请重新选择后重试。',
  errVaultUnconfigured: '保险库未配置 master key,无法引用 secret 凭据。',
}
