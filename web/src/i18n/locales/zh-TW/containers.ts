export default {
  title: '容器',
  subtitle: '跨所有已登記伺服器,按主機管理容器與映像',
  countSummary: '· 共 {total} 個容器 · {running} 執行中',
  autoRefresh: '· 每 {n} 秒自動重新整理',

  aiAssistant: '✦ AI 助手',
  prune: '🧹 清理',
  bulkEnter: '批次',
  bulkExit: '退出批次',
  create: '+ 新增容器',

  loadingAria: '正在載入容器清單',
  errTitle: '載入容器清單失敗',
  errConnect: '無法連線到伺服器,請檢查後端是否執行後重試',
  errLoadStatus: '載入容器清單失敗({status})',
  errLoadRetry: '載入容器清單失敗,請稍後重試',

  emptyTitle: '尚無已登記伺服器',
  emptyDesc: '先在「設定 › 伺服器」登記目標伺服器,這裡就會彙整它們上面的容器與映像。',

  kpiTotal: '容器總數',
  kpiRunning: '執行中',
  kpiStopped: '已停止',
  kpiHosts: '有容器的伺服器',
  kpiStripAria: '容器彙整統計',

  filterAria: '依狀態篩選容器',
  filterAll: '全部',
  filterRunning: '執行中',
  filterStopped: '已停止',
  filterPaused: '已暫停',

  searchPlaceholder: '依名稱 / 映像搜尋容器',
  searchAria: '依名稱或映像搜尋容器',
  searchClear: '清除搜尋',

  bulkAria: '批次操作',
  bulkSelected: '已選',
  bulkSelectedUnit: '個',
  bulkClear: '清空所選',
  actionStart: '啟動',
  actionStop: '停止',
  actionRestart: '重啟',
  actionDelete: '刪除',

  confirmTitle: '批次{label} {n} 個容器?',
  confirmBodyRm: '將對所選容器執行刪除(docker rm)。執行中的容器需先停止,否則會刪除失敗(計入失敗數)。',
  confirmBodyAction: '將對所選 {n} 個容器執行{label}操作,期間相關服務可能短暫中斷。',
  confirmLabel: '{label} {n} 個',

  toastDone: '批次{label}完成',
  toastDoneDetail: '成功 {n} 個',
  toastFail: '批次{label}失敗',
  toastFailDetail: '失敗 {n} 個',
  toastPartial: '批次{label}部分完成',
  toastPartialDetail: '成功 {ok} · 失敗 {fail}',

  cardsAria: '逐台伺服器的容器卡片',
  aiContextContainer: '(docker 主機)',
}
