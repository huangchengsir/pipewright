export default {
  groups: {
    resources: "リソースとリリース",
    integrations: "連携と AI",
    security: "セキュリティとアカウント",
    system: "システムと通知",
  },
  close: "閉じる",
  cancel: "キャンセル",
  confirm: "確認して続行",
  save: "保存",
  edit: "編集",
  delete: "削除",
  remove: "取り除く",
  name: "名前",
  select: "選択してください",
  none: "なし",
  loading: "読み込み中…",
  enabled: "有効",
  disabled: "無効",
  enable: "有効にする",
  disable: "無効にする",
  credentials: "Kubernetes 認証情報",
  addCredential: "Kubernetes 認証情報を追加",
  rotate: "認証情報を更新",
  authentication: "認証方式",
  serverToken: "サーバーとトークン",
  context: "コンテキスト",
  server: "API サーバー URL",
  ca: "CA 証明書（PEM）",
  token: "アクセストークン",
  tlsName: "TLS サーバー名（任意）",
  preview: "形式を確認",
  credential: "認証情報",
  identity: "技術的な詳細",
  addConnection: "接続を追加",
  editConnection: "接続を編集",
  emptyConnections: "クラスターに接続",
  manageCredentials: "クラスター認証情報を管理",
  grants: "許可された対象",
  addGrant: "権限を追加",
  grantTitle: "対象権限を設定",
  project: "プロジェクト",
  environment: "環境",
  namespace: "名前空間",
  deployment: "Deployment",
  container: "コンテナー",
  containers: "許可するコンテナー",
  executor: "実行先",
  executors: "許可する実行先",
  local: "ローカル Docker",
  sshServer: "SSH サーバー",
  addSSH: "SSH 実行先を追加",
  hostKey: "SSH ホスト指紋",
  profile: "ツール設定",
  toolImage: "ツールイメージ（digest 固定）",
  architecture: "アーキテクチャー",
  kubectlPath: "kubectl パス",
  registryCredential: "レジストリ認証情報（任意）",
  approvalRequired: "リリース承認が必要",
  approvalOptional: "パイプラインの承認設定を使用",
  probe: "接続を確認",
  replicas: "準備完了レプリカ",
  target: "リリース対象",
  connection: "クラスター接続",
  manageConnections: "接続と権限を管理",
  jobTitle: "Kubernetes デプロイ",
  imageSource: "イメージの取得元",
  more: "その他の設定",
  timeout: "準備完了の待機時間（秒）",
  passwordTitle: "本人確認",
  password: "現在のアカウントパスワード",
  unsaved: "未保存の変更を破棄しますか？",
  discard: "変更を破棄",
  receipt: "クラスターのリリース結果",
  reconcile: "クラスター状態を確認",
  retire: "ローカル追跡を終了",
  retired: "追跡終了",
  operation: "操作 ID",
  sourceRun: "元の実行",
  intent: "リリース内容ハッシュ",
  revision: "リビジョン",
  reason: "診断コード",
  phases: "リリース手順",
  recorded: "記録あり",
  notRecorded: "記録なし",
  credentialsHint:
    "Git・SSH とは分けて管理する、クラスター専用の認証情報です。",
  emptyCredentials: "Kubernetes 認証情報は未設定です。",
  secretHint:
    "保存・キャンセル後に秘密情報を消去します。更新時は全情報を再入力してください。平文表示はできません。",
  previewReady: "形式は有効です。クラスター接続は未確認です",
  connectionsHint:
    "信頼済み接続と明示的な権限で既存の Deployment にリリースします。",
  setupHint:
    "保管庫に認証情報を追加し、ツールイメージと許可対象を設定してください。",
  grantHint:
    "指定したプロジェクト、環境、ワークロード、コンテナー、実行先のみを許可します。",
  emptyGrants:
    "許可対象がありません。接続確認やリリース前に権限を追加してください。",
  containersHint:
    "名前を半角カンマで区切ります。ワイルドカードは使用できません。",
  hostKeyHint:
    "信頼できる経路で SHA256 指紋を確認してください。接続時に完全一致を検証します。",
  profileHint:
    "保存だけではツールは検証されません。接続確認で実行環境を検証します。",
  probeHint:
    "選択した許可対象を読み取ります。イメージ更新やリリースは行いません。",
  probeReady: "接続を確認し、対象を読み取りました",
  jobDescription: "既存の Deployment のイメージを更新し、準備完了を待ちます。",
  separateStage:
    "行列・並列ノード・追加タスクのない専用段階が必要です。「ノードを追加」で作成してください。",
  noProjectGrant:
    "このプロジェクトの権限がありません。設定で追加してください。",
  staleTarget: "以前の対象は選択できません。権限を確認してください",
  unavailableChoice: "以前の選択は利用できません",
  imageSourceHint:
    "先行するイメージ送信ノードを選択します。実行時は実際のレジストリ digest を使用します。",
  noPushSource:
    "先行するイメージ送信ノードがありません。追加して接続してください。",
  passwordHint:
    "この操作には直近 5 分以内のアカウントパスワード確認が必要です。",
  unsavedBody: "離れるとフォームの内容は消去されます。",
  deleteBody: "「{name}」を削除しますか？使用中の対象は削除できません。",
  noReceipt:
    "このノードの一意な結果がありません。成功や再試行可否は確認できません。",
  retireBody:
    "新しい完全リリースを許可するため追跡を終了します。ロールバックや元の実行の成功への変更は行いません。",
  unknownHint:
    "リリースが反映済みの可能性があります。再送信せず先に確認してください。",
  cleanupHint:
    "ツールコンテナーのクリーンアップが未確認のため再試行できません。",
  historyHint:
    "確認結果は元の実行記録を変更しません。次の実行では対象を再検証します。",
  notRecordedHint: "この手順の記録がないため、開始・成功は確認できません。",
  noRollback:
    "Kubernetes 対象を含むため SSH ロールバックは使えません。新しい完全実行で目的のバージョンをリリースしてください。",
  adopted: "元のリリースを再検証して再利用",
  rolloutOnly:
    "デプロイの準備完了のみを確認しています。継続監視ではありません。",
  phase: {
    preflight: "事前確認",
    update: "イメージ更新",
    rollout: "準備完了を待機",
    cleanup: "ツールの削除",
  },
  phaseCode: {
    patch_not_authorized:
      "このクラスタ認証情報では Deployment を更新できません。管理者に権限の確認を依頼してください。更新は送信されていません。",
    permission_check_unverified:
      "クラスタへのデプロイ権限を確認できませんでした。接続と権限確認の設定を確認してください。更新は送信されていません。",
    preflight_started: "権限、ツール環境、対象の確認を開始。",
    image_update_dispatched:
      "イメージ更新を送信しました。最終結果は受信記録で確認します。",
    rollout_observing: "対象レプリカの準備状態を監視中。",
    rollout_ready: "今回の観察でワークロードの準備完了を確認。",
    cleanup_started: "今回のツールコンテナーの削除を開始。",
    cleanup_verified: "ツールの削除完了を確認。",
    cleanup_unverified:
      "ツールの削除が未確認です。受信記録を確認してください。",
  },
  business: {
    not_dispatched: "リリース準備中",
    submitted: "送信済み、準備完了待ち",
    ready: "ワークロード準備完了",
    unchanged: "同じバージョンです。準備完了を確認済み",
    failed: "リリース未完了",
    superseded: "別の変更に置き換えられました",
    outcome_unknown: "リリース結果は未確認",
  },
  cleanup: {
    pending: "削除確認待ち",
    verified: "削除確認済み",
    blocked: "削除がブロック中",
  },
  errors: {
    invalid: "設定の形式が無効です。項目を確認してください。",
    session: "セッションが無効です。再ログインしてください。",
    passwordRequired: "操作前にパスワードを再確認してください。",
    passwordInvalid:
      "パスワード確認に失敗しました。確認して再試行してください。",
    denied: "接続または対象が許可されていません。",
    missing: "記録がありません。一覧を更新してください。",
    conflict:
      "設定が変更されています。下書きは保持しました。更新して確認後に保存してください。",
    inUse: "接続または実行が参照中のため、まだ変更できません。",
    held: "未完了のリリースが対象を使用中です。元の操作を確認してください。",
    ownerUnknown:
      "元の実行先の終了が未確認のため、引き継ぎや再試行はできません。",
    runtime:
      "実行環境の安全確認に失敗しました。ツールと実行先の要件を確認してください。",
    credentialUnavailable:
      "クラスター認証情報が使えません。保管庫と接続を確認してください。",
    observation:
      "状態確認が中断され結果は未確認です。元の操作記録を確認してください。",
    unavailable: "実行サービスを利用できません。接続条件を確認してください。",
    uncertain:
      "結果は不明です。再送信を停止しました。記録を更新して確認してください。同名の記録だけでは作成成功を証明できません。",
    load: "読み込めませんでした。更新して再試行してください。",
  },
  readBack: "記録を読み取り確認",
  acknowledge: "確認済み、編集を終了",
  readBackHint:
    "最新記録を読み込みました。対象と版を確認してください。元の要求の成否は自動判定しません。",
  targetCount: "{n} 件のリリース対象",
};
