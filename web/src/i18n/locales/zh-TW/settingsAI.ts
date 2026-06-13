export default {
  title: 'AI 服務商',
  subtitle:
    'Pipewright 不自訓模型 —— 接入你自己的 LLM 用於失敗診斷與設定生成。金鑰僅存於本實例的加密保險庫,絕不外洩。',
  statusConfigured: '已設定',
  statusUnconfigured: '未設定',
  retry: '重試',

  providerClaudeTag: '診斷推薦',
  providerOllamaDesc: '本機 / 自架',
  providerOllamaTag: '零外送',

  guidanceAria: 'AI 設定引導',
  guidanceTitle: '設定 LLM 以解鎖 AI 診斷',
  guidanceBody:
    '接入 Claude、OpenAI 或本機 Ollama 後,流水線失敗時 Pipewright 將自動產生根因假說與修復建議,無需手動排查日誌。',

  selectProvider: '選擇服務商',
  providerRadioAria: 'AI 服務商選擇',
  selectProviderAria: '選擇 {name}',
  providerConfig: '{name} 設定',
  lastSaved: '上次儲存 {time}',

  apiKeyHint: '寫入後僅顯示遮罩;留空則保留已存金鑰',
  apiKeyReplacing: '正在替換…',
  apiKeyConfigured: '已設定 ••••(留空不變)',
  apiKeyPaste: '貼上 API Key…',
  apiKeyMaskedAria: '已設定遮罩: {masked}',

  ollamaHint: '本機 Ollama 無需 API Key —— 確保 Ollama 服務已在指定位址執行即可。',

  baseUrlLabel: '接入位址 (Base URL)',
  baseUrlHint: '預設: {url}',

  modelLabel: '模型',
  modelHint: '用於診斷的主模型,如 claude-opus-4-7 / gpt-4o / llama3',

  testConnection: '測試連線',
  testOk: '連線正常 · 延遲 {ms}ms',
  testFail: '連線失敗',

  budgetLabel: '每月 Token 上限',
  budgetHint: '超出後暫停 AI 診斷(留空=不限制;本期僅宣告,下一 Epic 強制執行)',
  budgetPlaceholder: '如 500000,留空不限制',

  enableAi: '啟用 AI 功能',
  enableAiDesc: '關閉時 AI 診斷靜默略過,核心 CI/CD 流水線不受影響',

  dirtyNote: '有未儲存的變更',
  cleanNote: '暫無變更',
  discard: '捨棄',
  saveChanges: '儲存變更',

  toastSaveSuccess: 'AI 設定已儲存',
  toastSaveFailed: '儲存失敗',

  errServerUnreachable: '無法連線到伺服器,請檢查後端是否執行後重試',
  errServerUnreachableShort: '無法連線到伺服器',
  errVaultUnconfigured: '保險庫未設定 master key,請設定 PIPEWRIGHT_MASTER_KEY 環境變數',
  errLoadFailed: '載入失敗({status})',
  errLoadGeneric: '載入 AI 設定失敗,請稍後重試',
  errBudgetInvalid: '每月 token 上限須為正整數或留空',
  errProviderInvalid: '請選擇有效的服務商',
  errBaseUrlRequired: '請填寫接入位址',
  errApiKeyRequired: 'API Key 不可為空(非 Ollama 必填)',
  errRequestFailed: '請求失敗({status})',
  errUnknown: '未知錯誤',
}
