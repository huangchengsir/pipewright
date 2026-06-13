export default {
  // ─── page header ───────────────────────────────────────────────
  title: '專案',
  subtitle: '納管的 Gitee 倉庫,每個專案對應一套流水線設定與部署目標',
  newProject: '新建專案',
  retry: '重試',

  // ─── toolbar / search / filter ─────────────────────────────────
  searchFilterAria: '專案搜尋與篩選',
  searchPlaceholder: '搜尋專案名、倉庫位址、分支…',
  searchAria: '搜尋專案',
  statusFilterAria: '狀態篩選',
  statusAll: '全部狀態',

  // ─── list states ───────────────────────────────────────────────
  loading: '載入中',
  emptyTitle: '還沒有專案',
  emptyHint: '接入第一個 Gitee 倉庫,後續可設定流水線並部署到目標伺服器。',
  noMatchTitle: '沒有符合的專案',
  noMatchHint: '調整搜尋詞或狀態篩選條件後重試。',
  clearFilter: '清除篩選',
  resultCount: '{n} 個專案',
  resultCountTotal: '(共 {total} 個)',

  // ─── project card ──────────────────────────────────────────────
  runStatusAria: '執行狀態:{status}',
  lastRun: '上次執行',
  noRun: '尚無執行',
  targetServers: '目標伺服器',
  notBound: '未綁定',
  credential: '倉庫憑證',
  credentialRefTitle: '憑證引用(非明文)',
  updatedAt: '更新於 {time}',

  // ─── card actions ──────────────────────────────────────────────
  actionRunTitle: '手動觸發執行 · {name}',
  actionRunAria: '手動觸發專案 {name} 的流水線執行',
  actionRenameTitle: '重新命名 {name}',
  actionRenameAria: '重新命名專案 {name}',
  actionCodeTitle: '程式碼瀏覽 · {name}',
  actionCodeAria: '瀏覽專案 {name} 的程式碼',
  actionPipelineTitle: '流水線設定 · {name}',
  actionPipelineAria: '設定專案 {name} 的流水線',
  actionDeleteTitle: '刪除 {name}',
  actionDeleteAria: '刪除專案 {name}',

  // ─── shared modal ──────────────────────────────────────────────
  closeDialog: '關閉對話框',
  cancel: '取消',
  save: '儲存',
  saving: '儲存中…',

  // ─── trigger modal ─────────────────────────────────────────────
  triggerDialogAria: '手動觸發 · {name}',
  triggerTitle: '手動觸發執行',
  triggerSub: '{name} · 指定分支並立即建立一次流水線執行',
  branch: '分支',
  branchHint: '（可選,留空使用專案預設分支）',
  commit: 'Commit',
  commitHint: '（可選,留空使用分支 HEAD）',
  commitPlaceholder: '例:a3f1c2d',
  params: '參數',
  paramsHintTyped: '（依定義填寫,注入流水線作為環境變數）',
  paramsHintFree: '（可選,注入流水線作為環境變數）',
  triggering: '觸發中…',
  runNow: '立即執行',

  // ─── create modal ──────────────────────────────────────────────
  createSub: '接入 Gitee 倉庫並綁定倉庫憑證',
  fieldName: '專案名稱',
  fieldNamePlaceholder: '例:acme-web',
  fieldRepo: '倉庫位址',
  fieldCredHint: '（僅顯示 Git 權杖類型，不含明文）',
  credLoading: '載入憑證中…',
  credSelect: '選擇 Git 權杖憑證',
  credEmptyPre: '尚無 Git 權杖憑證,請先前往',
  credVaultLink: '憑證保險庫',
  credEmptyPost: '新增。',
  fieldDefaultBranch: '預設分支',
  fieldDefaultBranchHint: '（可選,留空由測試連線自動偵測）',
  testConnection: '測試連線',
  testing: '測試中…',
  testOk: '連線成功',
  testDetectedBranch: '· 預設分支 {branch}',
  creating: '建立中…',
  createSubmit: '建立專案',

  // ─── rename modal ──────────────────────────────────────────────
  renameTitle: '重新命名專案',
  renameSub: '修改專案的顯示名稱',

  // ─── delete modal ──────────────────────────────────────────────
  deleteDialogAria: '確認刪除專案',
  deleteTitle: '刪除專案',
  deleteSub: '此操作無法復原',
  deleteConfirmPre: '確定要永久刪除專案',
  deleteConfirmPost: '嗎?其流水線設定、執行歷史及憑證引用關係將一併清理。',
  deleting: '刪除中…',
  confirmDelete: '確認刪除',

  // ─── error messages ────────────────────────────────────────────
  errNetwork: '無法連線到伺服器,請檢查後端是否執行後重試。',
  errNetworkRetry: '無法連線到伺服器,請稍後重試。',
  errLoadNetwork: '無法連線到伺服器,請檢查後端是否執行後重試',
  errLoadStatus: '載入專案失敗({status})',
  errLoadRetry: '載入專案失敗,請稍後重試',
  errNameRequired: '請輸入專案名稱',
  errRepoRequired: '請輸入倉庫位址',
  errRepoFormat: '倉庫位址格式不正確,請以 https:// 或 git@ 開頭',
  errCredRequired: '請選擇倉庫憑證',
  errRepoFirst: '請先輸入倉庫位址',
  errCredFirst: '請先選擇倉庫憑證',
  errNameEmpty: '專案名稱不能為空',

  testErrCredential: '憑證錯誤:請檢查 Gitee 存取權杖是否有效,前往憑證保險庫更新。',
  testErrUnreachable: '倉庫不可達:請確認倉庫位址正確,且倉庫存在且可存取。',
  testErrVault: '保險庫未設定 master key,無法讀取憑證。',
  testErrStatus: '連線測試失敗({status})',
  testErrRetry: '連線測試失敗,請稍後重試。',

  createErrCredField: '憑證錯誤:請檢查存取權杖是否有效',
  createErrCredBanner: '憑證驗證失敗,請更換憑證或前往憑證保險庫更新。',
  createErrRepoField: '倉庫不可達:請確認位址正確且可存取',
  createErrRepoBanner: '倉庫位址不可達,建立失敗。',
  createErrVault: '保險庫未設定 master key,無法儲存專案。',
  createErrStatus: '建立失敗({status})',
  createErrRetry: '建立失敗,請稍後重試。',

  renameErrStatus: '重新命名失敗({status})',
  renameErrRetry: '重新命名失敗,請稍後重試。',

  deleteErrStatus: '刪除失敗({status})',
  deleteErrRetry: '刪除失敗,請稍後重試。',

  triggerErrNotFound: '專案不存在,請重新整理後重試。',
  triggerErrStatus: '觸發失敗({status})',
  triggerErrRetry: '觸發失敗,請稍後重試。',
}
