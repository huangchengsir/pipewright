export default {
  title: '容器',
  subtitle: '跨所有已登记服务器,按主机管理容器与镜像',
  countSummary: '· 共 {total} 个容器 · {running} 运行中',
  autoRefresh: '· 每 {n} 秒自动刷新',

  aiAssistant: '✦ AI 助手',
  prune: '🧹 清理',
  bulkEnter: '批量',
  bulkExit: '退出批量',
  create: '+ 新增容器',

  loadingAria: '正在加载容器列表',
  errTitle: '加载容器列表失败',
  errConnect: '无法连接到服务器,请检查后端是否运行后重试',
  errLoadStatus: '加载容器列表失败({status})',
  errLoadRetry: '加载容器列表失败,请稍后重试',

  emptyTitle: '尚无已登记服务器',
  emptyDesc: '先在「设置 › 服务器」登记目标服务器,这里就会汇总它们上面的容器与镜像。',

  kpiTotal: '容器总数',
  kpiRunning: '运行中',
  kpiStopped: '已停止',
  kpiHosts: '有容器的服务器',
  kpiStripAria: '容器聚合统计',

  filterAria: '按状态筛选容器',
  filterAll: '全部',
  filterRunning: '运行中',
  filterStopped: '已停止',
  filterPaused: '已暂停',

  searchPlaceholder: '按名字 / 镜像搜索容器',
  searchAria: '按名字或镜像搜索容器',
  searchClear: '清除搜索',

  bulkAria: '批量操作',
  bulkSelected: '已选',
  bulkSelectedUnit: '个',
  bulkClear: '清空所选',
  actionStart: '启动',
  actionStop: '停止',
  actionRestart: '重启',
  actionDelete: '删除',

  confirmTitle: '批量{label} {n} 个容器?',
  confirmBodyRm: '将对所选容器执行删除(docker rm)。运行中的容器需先停止,否则会删除失败(计入失败数)。',
  confirmBodyAction: '将对所选 {n} 个容器执行{label}操作,期间相关服务可能短暂中断。',
  confirmLabel: '{label} {n} 个',

  toastDone: '批量{label}完成',
  toastDoneDetail: '成功 {n} 个',
  toastFail: '批量{label}失败',
  toastFailDetail: '失败 {n} 个',
  toastPartial: '批量{label}部分完成',
  toastPartialDetail: '成功 {ok} · 失败 {fail}',

  cardsAria: '逐台服务器的容器卡片',
  aiContextContainer: '(docker 主机)',
}
