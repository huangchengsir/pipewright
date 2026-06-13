export default {
  title: 'DORA 指标',
  subtitle: '基于既有运行数据聚合的交付效能视图 · 部署频率 / 前置时长 / 变更失败率 / 故障恢复',
  generatedAt: '· 数据截至 {time}',

  window7d: '近 7 天',
  window30d: '近 30 天',
  window90d: '近 90 天',

  projectLabel: '项目',
  projectFilterAria: '按项目筛选',
  allProjects: '全部项目',
  windowAria: '时间窗口',

  errTitle: '加载 DORA 指标失败',
  errOffline: '无法连接到服务器,请检查后端是否运行后重试',
  errLoadStatus: '加载 DORA 指标失败({status})',
  errLoadRetry: '加载 DORA 指标失败,请稍后重试',

  summaryDeployments: '{days} 天内部署',
  summarySuccess: '成功',
  summaryFailed: '失败',

  metricDeployFreq: '部署频率',
  metricLeadTime: '变更前置时长',
  metricCfr: '变更失败率',
  metricMttr: '故障恢复时长',

  capDeployFreq: '{days} 天内 {count} 次成功部署',
  capLeadTime: '{count} 次成功部署的中位提交→投产时长',
  capLeadTimeEmpty: '尚无成功部署可统计前置时长',
  capCfr: '{failed} / {total} 次部署失败',
  capMttr: '{count} 段「失败→恢复」的中位时长',
  capMttrEmpty: '窗口内无「失败后恢复」配对',

  noteLead: '口径说明:一次「部署」= 一条进入终态的运行;前置时长在缺少提交时间时以入队时刻近似。DORA 指标基于 CI 运行数据为',
  noteEmphasis: '近似',
  noteTrail: '参考,不作 SLA 依据。',
}
