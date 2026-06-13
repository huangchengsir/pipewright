export default {
  title: 'サーバーの状態',
  subtitle: '登録済みのすべてのサーバーの CPU 負荷・メモリ・ディスク使用量を、SSH 経由でリアルタイムに収集',
  reachableSummary: '{reachable}/{total} 到達可能',
  autoRefresh: '{n} 秒ごとに自動更新',
  loadingAria: 'サーバーの状態を読み込み中',
  errTitle: 'サーバーの状態の読み込みに失敗しました',
  errConnect: 'サーバーに接続できません。バックエンドが稼働しているか確認してから再試行してください。',
  errLoadStatus: 'サーバーの状態の読み込みに失敗しました({status})',
  errLoadRetry: 'サーバーの状態の読み込みに失敗しました。しばらくしてから再試行してください。',
  emptyTitle: '登録済みのサーバーがまだありません',
  emptyDesc: '「設定 › サーバー」で対象サーバーを登録すると、ここにリソース指標が表示されます。',
}
