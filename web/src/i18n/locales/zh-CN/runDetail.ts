export default {
  title: '运行详情',
  runList: '运行列表',
  backToRunsAria: '返回运行列表',
  loadingAria: '加载运行详情中',
  retry: '重试',
  runNotFound: '运行 {id} 不存在',
  loadFailed: '加载失败({status})',
  loadRequestFailed: '加载运行详情失败,请稍后重试',

  // 状态徽标 aria
  statusAria: '运行状态:{status}',
  runStatusAria: '运行{status}',

  // 取消运行
  cancelRun: '取消运行',
  cancelling: '取消中…',
  cancelFailed: '取消失败({status})',
  cancelRequestFailed: '取消请求失败,请稍后重试',

  // 审批门
  approvalRegionAria: '等待人工审批',
  approvalTitle: '阶段「{stage}」等待人工审批',
  approvalSub: '批准后继续执行,拒绝则该阶段失败、运行终止。',
  reject: '拒绝',
  approve: '批准',
  approving: '处理中…',
  actionFailed: '操作失败({status})',
  approvalRequestFailed: '审批请求失败,请稍后重试',

  // 元信息条
  metaProject: '项目',
  metaBranch: '分支',
  metaCommit: 'Commit',
  metaTrigger: '触发',
  metaStarted: '开始',
  metaDuration: '耗时',
  triggerManual: '手动',
  commitUnresolved: '未取到',

  // 区块标题
  pipelineProgress: '流水线进度',
  pipelineComplete: '流水线完成',
  pipelineFailed: '流水线失败',
  stepRecord: '步骤记录',

  // 日志区 aria
  liveLogAria: '实时日志终端',
  historyLogAria: '历史运行日志',
  failedLogAria: '失败运行日志',
  diffAria: '成功失败代码差异对比',

  // 成功汇总
  finishedTime: '完成时间',
  totalDuration: '总耗时',
  endTime: '结束时间',

  // 交互式分批部署
  batchPausedPrefix: '分批部署已暂停:首批已发布,其余 ',
  batchPausedSuffix: ' 台待确认',
  processing: '处理中…',
  continueRest: '继续部署其余',
  abortKeepOld: '中止(保留旧版本)',
  continueFailed: '续发失败({status})',
  continueRequestFailed: '续发请求失败,请稍后重试',
  abortFailed: '中止失败({status})',
  abortRequestFailed: '中止请求失败,请稍后重试',
  noArtifactContinue: '无可用产物,无法续发',

  // 部署入口 / 面板
  deployToServers: '部署到目标服务器',
  deployAgain: '再次部署',
  deployConfigAria: '部署配置',
  deployCloseAria: '收起部署面板',
  deployArtifact: '部署产物',
  targetServers: '目标服务器',
  noServers: '暂无已登记的服务器,请先在「服务器」页登记。',

  // 健康检查
  healthCheck: '健康检查',
  hcNone: '不检查(命令成功即视为部署成功)',
  hcHttp: 'HTTP 探测(curl)',
  hcCommand: '命令探测',
  hcUrlAria: '健康检查 URL',
  hcCommandPlaceholder: '例:systemctl is-active shop',
  hcCommandAria: '健康检查命令',
  hcRetries: '重试次数',
  hcInterval: '间隔(秒)',
  hcTimeout: '超时(秒)',
  hcUrlRequired: '请填写探测 URL。',
  hcCommandRequired: '请填写探测命令。',

  // 发布策略
  releaseStrategy: '发布策略',
  strategyRollingLabel: '滚动',
  strategyRollingDesc: '全机并行,各自成败',
  strategyCanaryLabel: '金丝雀',
  strategyCanaryDesc: '先发小批,通过再铺',
  strategyBlueGreenLabel: '蓝绿',
  strategyBlueGreenDesc: '统一切换,失败全退',
  strategyInteractiveLabel: '交互式分批',
  strategyInteractiveDesc: '先发首批,暂停等人确认',
  canaryCount: '金丝雀台数',
  canaryHint: '先发这么多台并健康门控,通过后再铺其余',
  blueGreenHint: '全机先就绪发布目录、再统一原子切换;任一机切换失败则整个机群回滚到上一发布(dist / jar)。',

  // 零停机高级选项
  advancedToggle: '高级:零停机发布选项',
  releaseBase: '发布根目录',
  releaseBasePlaceholder: '留空 → 后端从部署路径推导(<base>/releases/<runId> + <base>/current)',
  keepReleases: '保留旧发布份数',
  advancedHint: 'dist / jar 部署到版本化发布目录并原子切换 current 软链;健康检查失败时自动回滚到上一发布。',

  // 触发部署
  startDeploy: '开始部署',
  deploying: '部署中…',
  selectedCount: '已选 {n} 台',
  deployFailed: '部署失败({status})',
  deployRequestFailed: '部署请求失败,请稍后重试',

  // 重试失败目标
  noArtifactRetry: '无可用产物,无法重试',
  retryFailed: '重试失败({status})',
  retryRequestFailed: '重试请求失败,请稍后重试',

  // partial_failed
  partialInfo: '部分目标失败,失败台已独立回滚;其余台继续运行,互不连累。',
  multiTargetAria: '多机目标状态',
  multiTargetFanout: '多机目标扇出',
  noMultiResult: '暂无多机部署结果',
}
