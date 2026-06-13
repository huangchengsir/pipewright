export default {
  // ─── page header ───────────────────────────────────────────────
  title: 'プロジェクト',
  subtitle: '管理対象の Gitee リポジトリ。各プロジェクトはパイプライン設定とデプロイ先に対応します',
  newProject: 'プロジェクトを新規作成',
  retry: '再試行',

  // ─── toolbar / search / filter ─────────────────────────────────
  searchFilterAria: 'プロジェクトの検索と絞り込み',
  searchPlaceholder: 'プロジェクト名、リポジトリ URL、ブランチで検索…',
  searchAria: 'プロジェクトを検索',
  statusFilterAria: 'ステータスで絞り込み',
  statusAll: 'すべてのステータス',

  // ─── list states ───────────────────────────────────────────────
  loading: '読み込み中',
  emptyTitle: 'プロジェクトがまだありません',
  emptyHint: '最初の Gitee リポジトリを接続すると、パイプラインを設定してターゲットサーバーへデプロイできます。',
  noMatchTitle: '一致するプロジェクトがありません',
  noMatchHint: '検索語またはステータスの絞り込み条件を調整して再試行してください。',
  clearFilter: '絞り込みをクリア',
  resultCount: '{n} 件のプロジェクト',
  resultCountTotal: '(全 {total} 件)',

  // ─── project card ──────────────────────────────────────────────
  runStatusAria: '実行ステータス:{status}',
  lastRun: '前回の実行',
  noRun: '実行履歴なし',
  targetServers: 'ターゲットサーバー',
  notBound: '未バインド',
  credential: 'リポジトリ認証情報',
  credentialRefTitle: '認証情報の参照（平文ではありません）',
  updatedAt: '{time}に更新',

  // ─── card actions ──────────────────────────────────────────────
  actionRunTitle: '手動で実行をトリガー · {name}',
  actionRunAria: 'プロジェクト {name} のパイプライン実行を手動でトリガー',
  actionRenameTitle: '{name} の名前を変更',
  actionRenameAria: 'プロジェクト {name} の名前を変更',
  actionCodeTitle: 'コードを閲覧 · {name}',
  actionCodeAria: 'プロジェクト {name} のコードを閲覧',
  actionPipelineTitle: 'パイプライン設定 · {name}',
  actionPipelineAria: 'プロジェクト {name} のパイプラインを設定',
  actionDeleteTitle: '{name} を削除',
  actionDeleteAria: 'プロジェクト {name} を削除',

  // ─── shared modal ──────────────────────────────────────────────
  closeDialog: 'ダイアログを閉じる',
  cancel: 'キャンセル',
  save: '保存',
  saving: '保存中…',

  // ─── trigger modal ─────────────────────────────────────────────
  triggerDialogAria: '手動トリガー · {name}',
  triggerTitle: '手動で実行をトリガー',
  triggerSub: '{name} · ブランチを指定してパイプライン実行をすぐに作成します',
  branch: 'ブランチ',
  branchHint: '（任意。空欄の場合はプロジェクトのデフォルトブランチを使用）',
  commit: 'Commit',
  commitHint: '（任意。空欄の場合はブランチの HEAD を使用）',
  commitPlaceholder: '例:a3f1c2d',
  params: 'パラメータ',
  paramsHintTyped: '（定義に従って入力。環境変数としてパイプラインに注入されます）',
  paramsHintFree: '（任意。環境変数としてパイプラインに注入されます）',
  triggering: 'トリガー中…',
  runNow: '今すぐ実行',

  // ─── create modal ──────────────────────────────────────────────
  createSub: 'Gitee リポジトリを接続し、リポジトリ認証情報をバインドします',
  fieldName: 'プロジェクト名',
  fieldNamePlaceholder: '例:acme-web',
  fieldRepo: 'リポジトリ URL',
  fieldCredHint: '（Git トークン種別のみ表示。平文は含みません）',
  credLoading: '認証情報を読み込み中…',
  credSelect: 'Git トークン認証情報を選択',
  credEmptyPre: 'Git トークン認証情報がまだありません。まず',
  credVaultLink: '認証情報ボールト',
  credEmptyPost: 'で追加してください。',
  fieldDefaultBranch: 'デフォルトブランチ',
  fieldDefaultBranchHint: '（任意。空欄の場合は接続テストで自動検出されます）',
  testConnection: '接続をテスト',
  testing: 'テスト中…',
  testOk: '接続に成功しました',
  testDetectedBranch: '· デフォルトブランチ {branch}',
  creating: '作成中…',
  createSubmit: 'プロジェクトを作成',

  // ─── rename modal ──────────────────────────────────────────────
  renameTitle: 'プロジェクトの名前を変更',
  renameSub: 'プロジェクトの表示名を変更します',

  // ─── delete modal ──────────────────────────────────────────────
  deleteDialogAria: 'プロジェクト削除の確認',
  deleteTitle: 'プロジェクトを削除',
  deleteSub: 'この操作は取り消せません',
  deleteConfirmPre: 'プロジェクト',
  deleteConfirmPost: 'を完全に削除してもよろしいですか？パイプライン設定、実行履歴、認証情報の参照関係がすべて削除されます。',
  deleting: '削除中…',
  confirmDelete: '削除を確認',

  // ─── error messages ────────────────────────────────────────────
  errNetwork: 'サーバーに接続できません。バックエンドが起動しているか確認して再試行してください。',
  errNetworkRetry: 'サーバーに接続できません。しばらくしてから再試行してください。',
  errLoadNetwork: 'サーバーに接続できません。バックエンドが起動しているか確認して再試行してください',
  errLoadStatus: 'プロジェクトの読み込みに失敗しました（{status}）',
  errLoadRetry: 'プロジェクトの読み込みに失敗しました。しばらくしてから再試行してください',
  errNameRequired: 'プロジェクト名を入力してください',
  errRepoRequired: 'リポジトリ URL を入力してください',
  errRepoFormat: 'リポジトリ URL の形式が正しくありません。https:// または git@ で始めてください',
  errCredRequired: 'リポジトリ認証情報を選択してください',
  errRepoFirst: '先にリポジトリ URL を入力してください',
  errCredFirst: '先にリポジトリ認証情報を選択してください',
  errNameEmpty: 'プロジェクト名は空にできません',

  testErrCredential: '認証情報エラー：Gitee アクセストークンが有効か確認し、認証情報ボールトで更新してください。',
  testErrUnreachable: 'リポジトリに到達できません：URL が正しく、リポジトリが存在しアクセス可能か確認してください。',
  testErrVault: 'ボールトに master key が設定されていないため、認証情報を読み取れません。',
  testErrStatus: '接続テストに失敗しました（{status}）',
  testErrRetry: '接続テストに失敗しました。しばらくしてから再試行してください。',

  createErrCredField: '認証情報エラー：アクセストークンが有効か確認してください',
  createErrCredBanner: '認証情報の検証に失敗しました。認証情報を変更するか、認証情報ボールトで更新してください。',
  createErrRepoField: 'リポジトリに到達できません：URL が正しくアクセス可能か確認してください',
  createErrRepoBanner: 'リポジトリ URL に到達できません。作成に失敗しました。',
  createErrVault: 'ボールトに master key が設定されていないため、プロジェクトを保存できません。',
  createErrStatus: '作成に失敗しました（{status}）',
  createErrRetry: '作成に失敗しました。しばらくしてから再試行してください。',

  renameErrStatus: '名前の変更に失敗しました（{status}）',
  renameErrRetry: '名前の変更に失敗しました。しばらくしてから再試行してください。',

  deleteErrStatus: '削除に失敗しました（{status}）',
  deleteErrRetry: '削除に失敗しました。しばらくしてから再試行してください。',

  triggerErrNotFound: 'プロジェクトが存在しません。更新して再試行してください。',
  triggerErrStatus: 'トリガーに失敗しました（{status}）',
  triggerErrRetry: 'トリガーに失敗しました。しばらくしてから再試行してください。',
}
