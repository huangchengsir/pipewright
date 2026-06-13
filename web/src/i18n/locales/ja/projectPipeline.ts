export default {
  // ─── トップバー / パンくず ─────────────────────────────────────
  breadcrumbAria: 'パンくずナビゲーション',
  breadcrumbProjects: 'プロジェクト',
  title: 'パイプライン設定',

  // ─── タブ ───────────────────────────────────────────────────────
  tabCanvas: 'パイプラインキャンバス',
  tabVars: '変数とキャッシュ',
  tabTriggers: 'トリガー設定',
  tabEnvs: '環境と認証情報',
  tabStripAria: 'パイプライン設定タブ',

  // ─── パイプライン・アズ・コード(GitOps · FR-8-12)──────────────
  pacTitle: 'パイプライン・アズ・コード',
  pacOnHint: '実行は実行ブランチのリポジトリ .pipewright.yml を読み込んで駆動します(ファイルがない/不正な場合はこの設定にフォールバック)。',
  pacOffHint: '有効にすると、各実行は実行ブランチのリポジトリルートの .pipewright.yml を読み込んで駆動します(ない/不正な場合はこの設定にフォールバック)。',
  pacToggleFailed: '切り替えに失敗しました。再試行してください。',

  // ─── ツールバーボタン ───────────────────────────────────────────
  aiGenerate: 'AI でパイプライン生成',
  importYaml: 'YAML からインポート',
  templates: 'テンプレート',
  validate: '設定を検証',
  closeValidationPanel: '検証パネルを閉じる',
  badgeReady: '準備完了',
  badgeErrors: '{n} 件のエラー',
  saving: '保存中…',
  saveDraft: '下書きを保存',

  // ─── バナー / ステータス ────────────────────────────────────────
  dismiss: '閉じる',
  draftSaved: 'パイプラインの下書きを保存しました',
  retry: '再試行',
  loading: '読み込み中',

  // ─── 読み込みエラー ─────────────────────────────────────────────
  errNoServer: 'サーバーに接続できません。バックエンドが稼働しているか確認して再試行してください。',
  errProjectNotFound: 'プロジェクトが存在しません。プロジェクト ID が正しいか確認してください。',
  errLoadFailedStatus: 'パイプラインの読み込みに失敗しました（{status}）',
  errLoadFailedRetry: 'パイプラインの読み込みに失敗しました。しばらくしてから再試行してください。',

  // ─── 保存エラー ─────────────────────────────────────────────────
  errSaveFailedRetry: '保存に失敗しました。しばらくしてから再試行してください。',
  errSaveFailedStatus: '保存に失敗しました（{status}）',
  errInvalidStage: 'ステージ名は空にできず、kind は許可された値である必要があります。確認して再試行してください。',
  errInvalidJob: 'ジョブ名または種類は空にできません。入力して再試行してください。',
  errDuplicateId: 'ステージまたはジョブの ID が重複しています。重複を削除して再試行してください。',
  errInvalidBuild: 'ビルドモデルは dockerfile/toolchain、成果物の種類は image/jar/dist である必要があります。',
  errInvalidVar: '変数キーは空にできず、同一スコープ内で重複できません。secret 変数にはボールトの認証情報を選択する必要があります。',
  errInvalidEnvironment: '環境名は空にできず、イメージレジストリの種類は harbor/acr/dockerhub/custom である必要があります。',
  errCredentialNotFound: '参照されているボールトの認証情報が存在しません。選び直して再試行してください。',
  errVaultUnconfigured: 'ボールトに master key が設定されていないため、secret 認証情報を参照できません。',
}
