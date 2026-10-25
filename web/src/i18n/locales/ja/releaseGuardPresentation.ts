export default {
  aiTitle: 'AI 解説',
  boundaryDetails: 'レポートの範囲と証拠の保存',
  titles: {
    notFound: '取得時にコンテナが存在しないことを確認',
    unknown: 'この証拠はまだ解釈できません',
    unavailable: '有効な稼働サンプルがありません',
    running: '取得時にコンテナが稼働',
    exited: '取得時にコンテナが停止',
    oom: 'メモリ不足による終了を記録',
    image: '予定イメージの識別情報を記録',
    gateFailed: 'デプロイ時のヘルス判定が失敗',
    logs: '上限付きの観測期間ログを保存',
    unhealthy: 'サンプル取得時に Docker ヘルスチェックが失敗',
    logsUnavailable: '観測期間のログを取得できませんでした',
    logsPartial: '観測期間のログは一部のみ保存',
    logsEmpty: 'ログ期間を取得。返された行はありません',
  },
  logExcerpt: 'ログ抜粋（最大600文字。全文は技術詳細）',
  snapshotScope:
    '送信ごとに選択・承認したスナップショットのみを解説します。全レポートの分析や自動調査は行いません。',
  nextTitle: '次の操作',
  nextDescription: '失敗の証拠を確認し、変更調査や再観測を検討します。',
  investigateAction: '関連する変更を調査',
  why: '検証が失敗した理由',
  failedFallback:
    'ルールは失敗を確認しました。詳細な条件は不明です。技術情報を確認してください。',
  unknownTitle: '未確認の項目',
  unknownScope:
    'サンプルは継続稼働や業務の正常性を証明しません。根本原因には独立した証拠が必要です。',
  unconfirmed: '未確認の情報です。元の記録は技術詳細に保存されています。',
  unknown: '不明',
  unknownTarget: '未確認の対象',
  unknownCondition: '不明な検証条件',
  reportMissing: 'レポートが見つかりません。',
  technicalDetails: '技術詳細',
  reportDetails: 'レポートの元データ',
  evidenceReference: '証拠 {n} を表示',
  evidenceLoaded: '{n} 件読込済み。読み込み済みの証拠のみの件数です。',
  continueFinding: 'この証拠の検索を続ける',
  find: {
    found: '引用された証拠が見つかりました。',
    pending: '証拠は未読込です。ページごとに検索を続けられます。',
    loading: '証拠を読み込み、検索中…',
    missing: '最終ページまで検索しましたが見つかりません。',
    expired: '証拠は期限切れです。過去の結論は保持されています。',
    error: '読込に失敗しました。手動で検索を続けられます。',
    stalled:
      'ページ位置が進まないため検索を停止しました。レポートを更新してください。',
  },
  kinds: {
    runtime: '稼働サンプル',
    baseline: 'デプロイ前の基準',
    expected_image: '予定イメージの記録',
    deployment_health_gate: 'デプロイ時のヘルス判定',
    logs: '観測期間のログ',
    unknown: '不明な証拠の種類',
  },
  running: '稼働状態',
  runningValue: '稼働中',
  stoppedValue: '停止中',
  exitCode: '終了コード',
  oom: 'メモリ不足による終了',
  restarts: '再起動回数',
  yes: 'あり',
  no: 'なし',
  health: 'Docker ヘルス状態',
  healthValues: {
    healthy: 'Docker チェック合格',
    unhealthy: 'Docker チェック失敗',
    starting: 'Docker チェック開始中',
    absent: '未設定。業務の正常性は不明',
    unknown: '不明',
  },
  imageIdentity: 'イメージ識別情報',
  imageRecorded: '識別値',
  recorded: '記録済み。技術詳細を参照',
  identityValues: {
    config_id: 'ローカル Docker Image ID',
    manifest: 'マニフェストダイジェスト',
    index: 'インデックスダイジェスト',
    repository_digest_unknown: 'ダイジェストの種類不明',
    unknown: '不明',
  },
  gate: 'デプロイ判定',
  failed: '失敗',
  windowStart: 'ログ期間の開始',
  windowEnd: 'ログ期間の終了',
  logGap: 'ログ収集間隔の欠落',
  fileStatus: {
    added: '追加',
    modified: '変更',
    deleted: '削除',
    renamed: '名前変更',
    copied: 'コピー',
    unknown: '状態不明',
  },
  timeline: {
    deploymentStarted: 'デプロイ試行を開始',
    activationStarted: 'コンテナの有効化を開始',
    deploymentFinished: 'デプロイ試行が終了',
    baselineFailing: 'デプロイ前の基準ですでに稼働失敗を記録',
    logsRequested: '上限付きログ収集を要求',
    logsCollected: '観測期間のログ収集結果を保存',
    logsUnavailable: '観測期間のログを取得できません',
    finished: '今回の観測タスクが終了',
    investigated: '変更関連の調査を更新',
    evidenceExpired: '元の証拠が期限切れ',
  },
  runtimeImageTruncated:
    'イメージのメタデータは切り詰められました。取得済みの稼働情報は保持されていますが、完全なイメージ識別情報は未確認です。',
  targetBindingChanged:
    '稼働情報の取得後に対象設定が変更されました。この情報では現在の対象の紐付けが有効か確認できません。',
  targetBindingUnknown:
    '対象変更フラグの形式が無効です。現在の対象の紐付けは未確認です。',
  logsPartialScope:
    '保存された抜粋のみを表示します。要求した期間には省略・切り詰められた内容がある可能性があります。',
  logLines: '取得したログ行数',
  reasons: {
    sourceCacheBusy:
      'ローカルのソースキャッシュが使用中のため比較できませんでした。',
    sourceCacheUnreadable:
      'ローカルのソースキャッシュを読み込めないため、比較は未確認です。',
    baseCommitNotCached:
      '基準コミットがローカルにないため、正確な比較ができません。',
    currentCommitNotCached:
      '現在のコミットがローカルにないため、正確な比較ができません。',
    logTimeGap:
      'ログ収集に時間的な欠落があります。欠落期間の正常稼働は確認できません。',
    serviceRestarted:
      'サービスの再起動により観測が中断されました。アプリケーションの失敗を意味しません。',
    resolvedSourceUnavailable:
      '検証可能な完全なソースコミットがないため、正確なソース比較はできません。',
    sourceRunUnavailable:
      '対応するソースの実行記録が利用できないか未検証のため、ソースコミットを確認できません。',
    sourceRevisionConflict:
      'ソースコミットの記録に矛盾があるため、正確なソース比較の根拠にできません。',
    sourceRevisionNotConfirmed:
      '実行記録とデプロイ記録でソースコミットの一致を確認できないため、ソース比較は利用できません。',
    currentDeploymentIdentityUnavailable:
      '現在のデプロイ識別記録が利用できないため、信頼できるデプロイ比較の基準を確認できません。',
    deploymentHistoryUnavailable:
      'デプロイ履歴が利用できないため、変更比較の基準を確認できません。',
  },
  citationLabel: '{candidate} · {context} · 証拠 {n}',
}
