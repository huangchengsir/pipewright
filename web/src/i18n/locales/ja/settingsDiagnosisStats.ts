export default {
  title: '診断フィードバックループ',
  subtitle:
    'AI 診断の品質指標 —— フィードバックが多いほど診断が正確になり、誤ったケースはナレッジベースの種になります',
  loading: '読み込み中…',
  retry: '再試行',

  emptyTitle: '診断フィードバックはまだありません',
  emptyHint: '失敗した実行の AI 診断パネルで 👍/👎 をタップすると、統計がここに集約されます',

  accuracy: '精度',
  accuracyAria: '精度 {pct}%',

  countTotal: 'フィードバック総数',
  countUp: '👍 役に立った',
  countDown: '👎 要改善',

  trendTitle: '最近の傾向',
  trendBarTitle: '{pct}% · {count} 件',
  countUnit: '{count} 件',

  correctionsTitle: '最近の修正（ナレッジベースの種）',
  correctionsEmpty: '👎 に付随する正しい根本原因はまだありません。',
  runLabel: '実行 {id}',

  errLoadFailed: '読み込みに失敗しました（{status}）',
  errLoadGeneric: '診断統計の読み込みに失敗しました。しばらくしてから再試行してください。',
}
