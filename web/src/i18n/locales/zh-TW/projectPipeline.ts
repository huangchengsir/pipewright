export default {
  // ─── 頂欄 / 麵包屑 ───────────────────────────────────────────────
  breadcrumbAria: '麵包屑導航',
  breadcrumbProjects: '專案',
  title: '流水線設定',

  // ─── 標籤頁 ─────────────────────────────────────────────────────
  tabCanvas: '流水線編排',
  tabVars: '變數與快取',
  tabTriggers: '觸發設定',
  tabEnvs: '環境與憑證',
  tabStripAria: '流水線設定標籤頁',

  // ─── 流水線即程式碼(GitOps · FR-8-12)─────────────────────────────
  pacTitle: '流水線即程式碼',
  pacOnHint: '執行時將依執行分支讀取倉庫 .pipewright.yml 驅動(檔案缺失或非法時回退至此處設定)。',
  pacOffHint: '開啟後,每次執行依執行分支讀取倉庫根 .pipewright.yml 驅動(缺失或非法則回退此處設定)。',
  pacToggleFailed: '切換失敗,請重試。',

  // ─── 工具列按鈕 ─────────────────────────────────────────────────
  aiGenerate: 'AI 生成流水線',
  importYaml: '從 YAML 匯入',
  templates: '範本',
  validate: '驗證設定',
  closeValidationPanel: '關閉驗證面板',
  badgeReady: '就緒',
  badgeErrors: '{n} 個錯誤',
  saving: '儲存中…',
  saveDraft: '儲存草稿',

  // ─── 橫幅 / 狀態 ────────────────────────────────────────────────
  dismiss: '關閉提示',
  draftSaved: '流水線草稿已儲存',
  retry: '重試',
  loading: '載入中',

  // ─── 載入錯誤 ───────────────────────────────────────────────────
  errNoServer: '無法連線到伺服器,請檢查後端是否執行後重試。',
  errProjectNotFound: '專案不存在,請確認專案 ID 正確。',
  errLoadFailedStatus: '載入流水線失敗({status})',
  errLoadFailedRetry: '載入流水線失敗,請稍後重試。',

  // ─── 儲存錯誤 ───────────────────────────────────────────────────
  errSaveFailedRetry: '儲存失敗,請稍後重試。',
  errSaveFailedStatus: '儲存失敗({status})',
  errInvalidStage: '階段名稱不能為空或 kind 不在允許值內,請檢查後重試。',
  errInvalidJob: '任務名稱或類型不能為空,請補充後重試。',
  errDuplicateId: '階段或任務 ID 重複,請刪除重複項後重試。',
  errInvalidBuild: '建置模型須為 dockerfile/toolchain,產物類型須為 image/jar/dist。',
  errInvalidVar: '變數鍵不能為空且同作用域內不可重複;secret 變數須選擇保險庫憑證。',
  errInvalidEnvironment: '環境名稱不能為空,映像倉庫類型須為 harbor/acr/dockerhub/custom。',
  errCredentialNotFound: '引用的保險庫憑證不存在,請重新選擇後重試。',
  errVaultUnconfigured: '保險庫未設定 master key,無法引用 secret 憑證。',
}
