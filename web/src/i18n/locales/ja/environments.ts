export default {
  title: '環境',
  subtitle: '環境ごとに集約したデプロイ履歴と現在のアクティブバージョン · ワンクリックで前回成功したデプロイにロールバック',
  project: 'プロジェクト',
  filterByProject: 'プロジェクトで絞り込み',
  selectProject: 'プロジェクトを選択してください',

  emptySelectTitle: 'プロジェクトを選択してください',
  emptySelectDesc: 'デプロイ履歴はプロジェクトごとに集約されます。まず上でプロジェクトを選択してください。',
  emptyTitle: 'このプロジェクトにはまだデプロイ履歴がありません',
  emptyDesc:
    '環境へのデプロイが実行されると(webhook のブランチマッピングで環境名が解決されデプロイが完了すると)、ここに環境ごとのタイムラインが表示されます。',

  errLoadTitle: 'デプロイ履歴の読み込みに失敗しました',
  errNetwork: 'サーバーに接続できません。バックエンドが稼働しているか確認して再試行してください。',
  errLoad: 'デプロイ履歴の読み込みに失敗しました({status})',
  errLoadRetry: 'デプロイ履歴の読み込みに失敗しました。しばらくしてから再試行してください。',

  active: 'アクティブ',
  activeVersionTitle: '現在のアクティブバージョン',
  noActiveVersion: 'アクティブバージョンなし',
  noFullSuccessTitle: '全て成功したデプロイはまだありません',
  targetCount: 'ターゲットホスト {n} 台',

  rollback: 'ロールバック',
  rollbackEnabledTitle: '前回成功したデプロイにロールバック',
  rollbackDisabledTitle: 'ロールバックできる前回の成功デプロイがありません',
  rollbackTitle: '環境「{env}」をロールバック',
  rollbackBody:
    'この環境を前回成功したデプロイ(実行 {commit} · {when})にロールバックし、その成果物を元のターゲットホストに再デプロイします。この操作は実際のデプロイを実行します。',
  rollbackConfirm: 'ロールバックを確認',
  rollbackFailedStatus: 'ロールバックに失敗しました({status})',
  rollbackFailedRetry: 'ロールバックに失敗しました。しばらくしてから再試行してください。',

  toastRolledBack: '環境「{env}」をロールバックしました',
  toastRolledBackDetail: '成果物を {n} 台のターゲットホストに再デプロイしました',
  toastRollbackPartial: '環境「{env}」のロールバックが一部失敗しました',
  toastRollbackPartialDetail: 'ターゲットホスト {failed}/{total} 台が失敗しました',
  toastRollbackFailed: '環境「{env}」のロールバックに失敗しました',

  timelineAria: '{env} のデプロイ履歴',
}
