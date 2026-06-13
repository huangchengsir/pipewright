export default {
  title: '服务器状态',
  subtitle: '所有已登记服务器的 CPU 负载、内存与磁盘使用,经 SSH 实时采集',
  reachableSummary: '{reachable}/{total} 可达',
  autoRefresh: '每 {n} 秒自动刷新',
  loadingAria: '正在加载服务器状态',
  errTitle: '加载服务器状态失败',
  errConnect: '无法连接到服务器,请检查后端是否运行后重试',
  errLoadStatus: '加载服务器状态失败({status})',
  errLoadRetry: '加载服务器状态失败,请稍后重试',
  emptyTitle: '尚无已登记服务器',
  emptyDesc: '先在「设置 › 服务器」登记目标服务器,这里就会显示它们的资源指标。',
}
