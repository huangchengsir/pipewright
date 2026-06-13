export default {
  title: '伺服器狀態',
  subtitle: '所有已登記伺服器的 CPU 負載、記憶體與磁碟使用,經 SSH 即時採集',
  reachableSummary: '{reachable}/{total} 可達',
  autoRefresh: '每 {n} 秒自動重新整理',
  loadingAria: '正在載入伺服器狀態',
  errTitle: '載入伺服器狀態失敗',
  errConnect: '無法連線到伺服器,請檢查後端是否執行後重試',
  errLoadStatus: '載入伺服器狀態失敗({status})',
  errLoadRetry: '載入伺服器狀態失敗,請稍後重試',
  emptyTitle: '尚無已登記伺服器',
  emptyDesc: '先在「設定 › 伺服器」登記目標伺服器,這裡就會顯示它們的資源指標。',
}
