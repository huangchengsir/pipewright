export default {
  title: 'DORA メトリクス',
  subtitle:
    '既存の実行データから集計したデリバリーパフォーマンスビュー · デプロイ頻度 / リードタイム / 変更失敗率 / 復旧時間',
  generatedAt: '· データ基準時刻 {time}',

  window7d: '直近 7 日',
  window30d: '直近 30 日',
  window90d: '直近 90 日',

  projectLabel: 'プロジェクト',
  projectFilterAria: 'プロジェクトで絞り込み',
  allProjects: 'すべてのプロジェクト',
  windowAria: '期間',

  errTitle: 'DORA メトリクスの読み込みに失敗しました',
  errOffline: 'サーバーに接続できません。バックエンドが稼働しているか確認して再試行してください',
  errLoadStatus: 'DORA メトリクスの読み込みに失敗しました（{status}）',
  errLoadRetry: 'DORA メトリクスの読み込みに失敗しました。しばらくしてから再試行してください',

  summaryDeployments: '{days} 日間のデプロイ',
  summarySuccess: '成功',
  summaryFailed: '失敗',

  metricDeployFreq: 'デプロイ頻度',
  metricLeadTime: '変更のリードタイム',
  metricCfr: '変更失敗率',
  metricMttr: '平均復旧時間',

  capDeployFreq: '{days} 日間に {count} 回の成功デプロイ',
  capLeadTime: '{count} 回の成功デプロイにおけるコミット→本番投入の中央値',
  capLeadTimeEmpty: 'リードタイムを集計できる成功デプロイがまだありません',
  capCfr: '{failed} / {total} 回のデプロイが失敗',
  capMttr: '{count} 件の「失敗→復旧」ペアの中央値',
  capMttrEmpty: 'この期間には「失敗後の復旧」ペアがありません',

  noteLead:
    '集計基準：1 回の「デプロイ」= 終了状態に至った 1 件の実行。コミット時刻が無い場合、リードタイムはキュー投入時刻で近似します。CI 実行データに基づく DORA メトリクスは',
  noteEmphasis: '近似',
  noteTrail: '値であり、参考用です。SLA の根拠にはしないでください。',
}
