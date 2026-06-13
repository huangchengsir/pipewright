export default {
  // ─── shared ──────────────────────────────────────────────────────────────
  loadingShort: '読み込み中…',

  // ─── AuditTimeline ───────────────────────────────────────────────────────
  audit: {
    title: '監査ログ',
    sub: '誰が · いつ · 何に',
    treeAria: '監査タイムライン',
    emptyLabel: 'まだ監査記録がありません',
    emptyHint: '認証情報やプロジェクトの作成・変更・削除、webhook シークレットのリセット、実行の手動トリガーなどの重要な操作はすべてここに記録されます。記録は改ざんできません。',
    loadMore: '監査記録をさらに読み込む →',
    via: 'Web コンソール',
    actorYou: 'あなた',
    verbCreate: '作成',
    verbUpdate: '変更',
    verbDelete: '削除',
    verbReset: 'リセット',
    verbAdd: '接続',
    verbTrigger: '手動トリガー',
    verbDefault: '操作',
    nounCredential: '認証情報',
    nounWebhookSecret: 'webhook 署名シークレット',
    nounProject: 'プロジェクト',
    nounRun: '実行',
    errConnect: 'サーバーに接続できません。バックエンドが稼働しているか確認して再試行してください',
    errLoad: '監査ログの読み込みに失敗しました({status})',
    errLoadRetry: '監査ログの読み込みに失敗しました。しばらくしてから再試行してください',
  },

  // ─── CodeTree / CodeTreeNode ─────────────────────────────────────────────
  tree: {
    aria: 'コードディレクトリツリー',
    fileAria: 'リポジトリファイルツリー',
    title: 'ファイル',
    refTitle: '現在の ref: {ref}',
    loadingDir: 'ディレクトリを読み込み中…',
    emptyRepo: '空のリポジトリ / ソースは現在読み込めません',
    emptyDir: '空のディレクトリ',
    errConnect: 'サーバーに接続できません',
    errNotFound: 'パスが存在しません',
    errLoad: '読み込みに失敗しました({status})',
    errLoadGeneric: '読み込みに失敗しました',
  },

  // ─── CodeViewer ──────────────────────────────────────────────────────────
  code: {
    viewAria: 'コード表示',
    editorAria: 'コードエディター(読み取り専用)',
    noFileSelected: 'ファイル未選択',
    truncated: '切り詰め済み',
    truncatedTitle: 'ファイルが大きすぎるため、先頭部分のみ表示します',
    idleTitle: '左側からファイルを選択して表示',
    idleSub: 'リポジトリのソースを読み取り専用で閲覧し、構文ハイライトを表示します。編集やコミットはできません。',
    binaryTitle: 'バイナリファイルはプレビューできません',
    degradedTitle: 'ソースは現在読み込めません',
    degradedSub: 'リポジトリのクローンに失敗したか、現在の環境からアクセスできません。しばらくしてから再試行するか、プロジェクトのリポジトリ設定を確認してください。',
    errTitle: 'ファイルの読み込みに失敗しました',
    fallbackRegionAria: 'コード内容(プレーンテキスト代替)',
    fallbackNote: '構文ハイライトコンポーネントの読み込みに失敗したため、プレーンテキスト表示に切り替えました。',
    errConnect: 'サーバーに接続できません',
    errNotFound: 'ファイルが存在しません',
    errLoad: '読み込みに失敗しました({status})',
    errLoadGeneric: '読み込みに失敗しました',
  },

  // ─── ConfirmDialog ───────────────────────────────────────────────────────
  confirm: {
    cancel: 'キャンセル',
    confirm: '確認',
    typeLabelPrefix: '',
    typeLabelSuffix: 'と入力して確認',
    typePlaceholder: '{text} と入力…',
    typeAria: '{text} と入力して操作を確認',
  },

  // ─── EmptyState ──────────────────────────────────────────────────────────
  empty: {
    defaultTitle: 'データなし',
  },

  // ─── ErrorState ──────────────────────────────────────────────────────────
  error: {
    defaultTitle: '読み込みに失敗しました',
    retry: '再試行',
    aiUnavailableAria: 'AI 機能は現在利用できません',
    aiTitle: 'AI 失敗診断',
    aiTag: '利用不可',
    aiDesc: 'LLM プロバイダーから応答がなかったため、今回は診断が生成されませんでした。実行結果とログは通常どおり記録され、コアの CI/CD には影響ありません。',
    confidenceLabel: '信頼度 {n}% · {level}',
    confidenceHigh: '高',
    confidenceMedium: '中',
    confidenceLow: '低',
  },

  // ─── ToastHost ───────────────────────────────────────────────────────────
  toast: {
    hostAria: '通知',
    itemAria: '{type} 通知: {title}',
    closeAria: '通知を閉じる: {title}',
  },
}
