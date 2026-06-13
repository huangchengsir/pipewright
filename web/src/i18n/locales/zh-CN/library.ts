export default {
  // ─── page header ───────────────────────────────────────────────
  title: '复用库',
  subtitle: '跨项目共享的流水线模板、变量组与自定义节点 · 一处定义,处处复用',
  newGroup: '+ 新建变量组',
  newStudioNode: '+ 新建工作室节点',

  // ─── segmented tabs ────────────────────────────────────────────
  segAria: '复用库分类',
  tabTemplates: '流水线模板',
  tabVariableGroups: '变量组',
  tabCustomNodes: '自定义节点',

  // ─── common ────────────────────────────────────────────────────
  retry: '重试',
  delete: '删除',
  edit: '编辑',
  cancel: '取消',
  save: '保存',
  saving: '保存中…',
  close: '关闭',
  remove: '移除',
  noDescription: '无说明',
  emptyValue: '空',
  updatedAt: '更新于 {time}',

  // ─── templates ─────────────────────────────────────────────────
  emptyTemplatesTitle: '还没有流水线模板',
  emptyTemplatesHint: '模板让你把一份流水线定义沉淀下来,在任意项目的流水线编辑器里一键应用。在项目流水线编辑器中可把当前流水线另存为模板。',
  stageCount: '{n} 阶段',

  // ─── variable groups ───────────────────────────────────────────
  emptyGroupsTitle: '还没有变量组',
  emptyGroupsHint: '把一组共享变量(如同一套环境地址、令牌引用)定义为变量组,在多条流水线间复用。secret 变量只保存保险库引用,绝不存明文。',
  varCount: '{n} 变量',
  secretRefTitle: '保险库引用,明文不可见',
  moreVars: '+{n} 个变量…',
  noVars: '无变量',

  // ─── custom nodes ──────────────────────────────────────────────
  emptyNodesTitle: '还没有自定义节点',
  emptyNodesHint: '点右上「新建工作室节点」用低代码方式组合步骤 + 提升参数,沉淀成可复用节点;或在流水线编辑器里把任意节点配好参数后点「存为自定义节点」。之后在任意流水线的节点选择器一键复用。',
  moreParams: '+{n} 个参数…',
  noParams: '无参数',

  // ─── variable-group editor ─────────────────────────────────────
  createGroup: '新建变量组',
  editGroup: '编辑变量组',
  fieldName: '名称',
  fieldDescriptionOptional: '说明(可选)',
  fieldVariables: '变量',
  addVariable: '+ 添加变量',
  groupNamePlaceholder: '如 prod-shared-env',
  groupDescPlaceholder: '这组变量的用途',
  selectCredential: '选择凭据…',
  secretToggleOn: '保险库 secret(点击切回明文)',
  secretToggleOff: '明文(点击切为 secret)',

  // ─── custom-node editor ────────────────────────────────────────
  editNode: '编辑自定义节点',
  fieldSummaryOptional: '摘要(可选)',
  fieldUnderlyingType: '底层类型',
  underlyingTypeHint: '底层任务类型不可更改',
  fieldParams: '参数',
  addParam: '+ 添加参数',
  nodeNamePlaceholder: '如 build-and-push',
  nodeDescPlaceholder: '这个节点的用途',
  nodeSummaryPlaceholder: '一句话摘要,展示在卡片上',
  noParamsHint: '暂无参数,点「+ 添加参数」新增。',

  // ─── confirms / toasts / errors ────────────────────────────────
  confirmDeleteTemplate: '删除模板「{name}」?此操作不可撤销。',
  deletedTemplate: '已删除模板「{name}」',
  confirmDeleteGroup: '删除变量组「{name}」?此操作不可撤销。',
  deletedGroup: '已删除变量组「{name}」',
  createdGroup: '已创建变量组「{name}」',
  updatedGroup: '已更新变量组「{name}」',
  confirmDeleteNode: '删除自定义节点「{name}」?此操作不可撤销。',
  deletedNode: '已删除自定义节点「{name}」',
  updatedNode: '已更新自定义节点「{name}」',
  groupNameRequired: '变量组名称不能为空',
  nodeNameRequired: '自定义节点名称不能为空',
  deleteFailed: '删除失败',
  saveFailed: '保存失败',
  saveFailedStatus: '保存失败({status})',
  loadTemplatesFailed: '加载模板失败',
  loadTemplatesFailedStatus: '加载模板失败({status})',
  loadGroupsFailed: '加载变量组失败',
  loadGroupsFailedStatus: '加载变量组失败({status})',
  loadNodesFailed: '加载自定义节点失败',
  loadNodesFailedStatus: '加载自定义节点失败({status})',
}
