export default {
  title: 'コンテナ',
  subtitle: '登録済みの全サーバーを横断し、ホストごとにコンテナとイメージを管理',
  countSummary: '· 合計 {total} 個のコンテナ · {running} 起動中',
  autoRefresh: '· {n} 秒ごとに自動更新',

  aiAssistant: '✦ AI アシスタント',
  prune: '🧹 クリーンアップ',
  bulkEnter: '一括',
  bulkExit: '一括を終了',
  create: '+ コンテナを新規作成',

  loadingAria: 'コンテナ一覧を読み込み中',
  errTitle: 'コンテナ一覧の読み込みに失敗しました',
  errConnect: 'サーバーに接続できません。バックエンドが起動しているか確認して再試行してください。',
  errLoadStatus: 'コンテナ一覧の読み込みに失敗しました({status})',
  errLoadRetry: 'コンテナ一覧の読み込みに失敗しました。しばらくしてから再試行してください。',

  emptyTitle: '登録済みのサーバーがまだありません',
  emptyDesc: '「設定 › サーバー」で対象サーバーを登録すると、それらのコンテナとイメージがここに集約されます。',

  kpiTotal: 'コンテナ総数',
  kpiRunning: '起動中',
  kpiStopped: '停止済み',
  kpiHosts: 'コンテナのあるサーバー',
  kpiStripAria: 'コンテナ集約統計',

  filterAria: '状態でコンテナを絞り込み',
  filterAll: 'すべて',
  filterRunning: '起動中',
  filterStopped: '停止済み',
  filterPaused: '一時停止',

  searchPlaceholder: '名前 / イメージでコンテナを検索',
  searchAria: '名前またはイメージでコンテナを検索',
  searchClear: '検索をクリア',

  bulkAria: '一括操作',
  bulkSelected: '選択中',
  bulkSelectedUnit: '個',
  bulkClear: '選択をクリア',
  actionStart: '起動',
  actionStop: '停止',
  actionRestart: '再起動',
  actionDelete: '削除',

  confirmTitle: '{n} 個のコンテナを一括{label}しますか?',
  confirmBodyRm: '選択したコンテナを削除します(docker rm)。起動中のコンテナは先に停止する必要があり、そうでない場合は削除に失敗します(失敗数にカウント)。',
  confirmBodyAction: '選択した {n} 個のコンテナに対して{label}を実行します。その間、関連サービスが一時的に中断する可能性があります。',
  confirmLabel: '{n} 個を{label}',

  toastDone: '一括{label}が完了しました',
  toastDoneDetail: '成功 {n} 個',
  toastFail: '一括{label}に失敗しました',
  toastFailDetail: '失敗 {n} 個',
  toastPartial: '一括{label}が一部完了しました',
  toastPartialDetail: '成功 {ok} · 失敗 {fail}',

  cardsAria: 'サーバーごとのコンテナカード',
  aiContextContainer: '(docker ホスト)',
}
