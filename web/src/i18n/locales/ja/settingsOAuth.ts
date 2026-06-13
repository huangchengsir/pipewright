export default {
  title: 'OAuth アプリ',
  sectionDesc:
    '各 git プラットフォームに OAuth アプリ(Client ID / Secret)を登録すると、ユーザーは認証情報ボールトからワンクリックでアカウントを「接続」し、PAT を手動で貼り付けることなくトークンを自動取得できます。Secret は保存時にマスクされ、書き込み後は読み出せません。',
  retry: '再試行',

  providerCustomLabel: 'セルフホスト',
  providerGiteeDesc: 'Gitee',
  providerCustomDesc: 'セルフホストの GitLab / Gitea など',

  statusEnabled: '有効',
  statusConfiguredIdle: '設定済み・無効',
  statusUnconfigured: '未設定',

  clientIdPlaceholder: 'git プラットフォームの OAuth アプリページで取得',
  secretOptional: '(書き込み後はマスクのみ表示)',
  secretPlaceholderKeep: '空欄のままにすると既存の Secret を保持します',
  secretPlaceholderNew: 'Client Secret を貼り付け…',
  secretStored: '保存済み:{masked}',

  toggleAria: '{provider} OAuth を有効化',
  toggleTitle: '接続を有効化',
  toggleDesc: '有効化すると、このプラットフォームの「接続」ボタンが認証情報ボールトに表示されます',

  lastSaved: '前回の保存 {time}',
  saveBtn: '保存',

  errNoServer: 'サーバーに接続できません。バックエンドが稼働しているか確認してから再試行してください',
  errVaultUnconfigured: 'ボールトに master key が設定されていません。PIPEWRIGHT_MASTER_KEY 環境変数を設定してください',
  errLoadStatus: '読み込みに失敗しました({status})',
  errLoadGeneric: 'OAuth アプリの読み込みに失敗しました。しばらくしてから再試行してください',

  errClientIdRequiredEnabled: '有効化時は Client ID が必須です',
  errSecretRequiredEnabled: '有効化時は Client Secret が必要です',
  errBaseUrlRequiredCustom: 'セルフホストインスタンスには Base URL が必要です',
  errClientIdRequired: 'Client ID を入力してください',
  errBaseUrlRequired: 'Base URL を入力してください',

  toastSaveFailed: '保存に失敗しました',
  toastSaved: 'OAuth アプリを保存しました',
  errNoServerShort: 'サーバーに接続できません',
  unknownError: '不明なエラー',

  justNow: 'たった今',
  minutesAgo: '{n} 分前',
  hoursAgo: '{n} 時間前',
  daysAgo: '{n} 日前',
}
