export default {
  title: 'AI プロバイダー',
  subtitle:
    'Pipewright は独自のモデルを学習しません —— 障害診断と設定生成には自前の LLM を接続します。キーはこのインスタンスの暗号化された保管庫にのみ保存され、外部には決して漏れません。',
  statusConfigured: '設定済み',
  statusUnconfigured: '未設定',
  retry: '再試行',

  providerClaudeTag: '診断におすすめ',
  providerOllamaDesc: 'ローカル / セルフホスト',
  providerOllamaTag: '外部送信なし',

  guidanceAria: 'AI 設定ガイド',
  guidanceTitle: 'AI 診断を有効化するには LLM を設定してください',
  guidanceBody:
    'Claude、OpenAI、またはローカルの Ollama を接続すると、パイプライン障害時に Pipewright が根本原因の仮説と修正案を自動生成し、手動でのログ調査が不要になります。',

  selectProvider: 'プロバイダーを選択',
  providerRadioAria: 'AI プロバイダーの選択',
  selectProviderAria: '{name} を選択',
  providerConfig: '{name} の設定',
  lastSaved: '前回の保存 {time}',

  apiKeyHint: '入力後はマスク表示のみ。空欄のままにすると既存のキーを保持します',
  apiKeyReplacing: '置き換え中…',
  apiKeyConfigured: '設定済み ••••（空欄で変更なし）',
  apiKeyPaste: 'API Key を貼り付け…',
  apiKeyMaskedAria: '設定済みマスク: {masked}',

  ollamaHint: 'ローカルの Ollama に API Key は不要です —— Ollama サービスが指定したアドレスで稼働していることを確認してください。',

  baseUrlLabel: '接続先 (Base URL)',
  baseUrlHint: 'デフォルト: {url}',

  modelLabel: 'モデル',
  modelHint: '診断に使用するメインモデル。例: claude-opus-4-7 / gpt-4o / llama3',

  testConnection: '接続テスト',
  testOk: '接続正常 · レイテンシ {ms}ms',
  testFail: '接続に失敗しました',

  budgetLabel: '月間 Token 上限',
  budgetHint: '超過すると AI 診断を一時停止します（空欄=無制限。今回は宣言のみ、次の Epic で強制適用）',
  budgetPlaceholder: '例: 500000、空欄で無制限',

  enableAi: 'AI 機能を有効化',
  enableAiDesc: 'オフの場合 AI 診断は静かにスキップされ、コアの CI/CD パイプラインには影響しません',

  dirtyNote: '未保存の変更があります',
  cleanNote: '変更なし',
  discard: '破棄',
  saveChanges: '変更を保存',

  toastSaveSuccess: 'AI 設定を保存しました',
  toastSaveFailed: '保存に失敗しました',

  errServerUnreachable: 'サーバーに接続できません。バックエンドが稼働しているか確認して再試行してください。',
  errServerUnreachableShort: 'サーバーに接続できません',
  errVaultUnconfigured: '保管庫に master key が設定されていません。PIPEWRIGHT_MASTER_KEY 環境変数を設定してください。',
  errLoadFailed: '読み込みに失敗しました（{status}）',
  errLoadGeneric: 'AI 設定の読み込みに失敗しました。しばらくしてから再試行してください。',
  errBudgetInvalid: '月間 token 上限は正の整数か空欄である必要があります',
  errProviderInvalid: '有効なプロバイダーを選択してください',
  errBaseUrlRequired: '接続先を入力してください',
  errApiKeyRequired: 'API Key は空にできません（Ollama 以外は必須）',
  errRequestFailed: 'リクエストに失敗しました（{status}）',
  errUnknown: '不明なエラー',
}
