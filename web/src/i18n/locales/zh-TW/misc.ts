export default {
  // ─── shared ──────────────────────────────────────────────────────────────
  loadingShort: '載入中…',

  // ─── AuditTimeline ───────────────────────────────────────────────────────
  audit: {
    title: '稽核日誌',
    sub: '誰 · 何時 · 對什麼',
    treeAria: '稽核時間線',
    emptyLabel: '還沒有稽核記錄',
    emptyHint: '建立、修改、刪除憑證或專案,重設 webhook 金鑰,手動觸發執行等敏感操作都會在此留痕,記錄不可竄改。',
    loadMore: '載入更多稽核記錄 →',
    via: 'Web 主控台',
    actorYou: '你',
    verbCreate: '新增',
    verbUpdate: '修改',
    verbDelete: '刪除',
    verbReset: '重設',
    verbAdd: '接入',
    verbTrigger: '手動觸發',
    verbDefault: '操作',
    nounCredential: '憑證',
    nounWebhookSecret: 'webhook 簽章金鑰',
    nounProject: '專案',
    nounRun: '執行',
    errConnect: '無法連線到伺服器,請檢查後端是否執行後重試',
    errLoad: '載入稽核日誌失敗({status})',
    errLoadRetry: '載入稽核日誌失敗,請稍後重試',
  },

  // ─── CodeTree / CodeTreeNode ─────────────────────────────────────────────
  tree: {
    aria: '程式碼目錄樹',
    fileAria: '儲存庫檔案樹',
    title: '檔案',
    refTitle: '目前 ref: {ref}',
    loadingDir: '載入目錄…',
    emptyRepo: '空儲存庫 / 原始碼暫不可讀',
    emptyDir: '空目錄',
    errConnect: '無法連線到伺服器',
    errNotFound: '路徑不存在',
    errLoad: '載入失敗({status})',
    errLoadGeneric: '載入失敗',
  },

  // ─── CodeViewer ──────────────────────────────────────────────────────────
  code: {
    viewAria: '程式碼檢視',
    editorAria: '程式碼編輯器(唯讀)',
    noFileSelected: '未選擇檔案',
    truncated: '已截斷',
    truncatedTitle: '檔案過大,僅顯示前綴部分',
    idleTitle: '從左側選擇檔案檢視',
    idleSub: '唯讀瀏覽儲存庫原始碼,語法高亮,無法編輯或提交。',
    binaryTitle: '二進位檔案,不可預覽',
    degradedTitle: '原始碼暫不可讀',
    degradedSub: '儲存庫複製失敗或目前環境無法存取。請稍後重試或檢查專案儲存庫設定。',
    errTitle: '檔案載入失敗',
    fallbackRegionAria: '程式碼內容(純文字降級)',
    fallbackNote: '語法高亮元件載入失敗,已降級為純文字檢視。',
    errConnect: '無法連線到伺服器',
    errNotFound: '檔案不存在',
    errLoad: '載入失敗({status})',
    errLoadGeneric: '載入失敗',
  },

  // ─── ConfirmDialog ───────────────────────────────────────────────────────
  confirm: {
    cancel: '取消',
    confirm: '確認',
    typeLabelPrefix: '輸入',
    typeLabelSuffix: '確認',
    typePlaceholder: '輸入 {text}…',
    typeAria: '輸入 {text} 確認操作',
  },

  // ─── EmptyState ──────────────────────────────────────────────────────────
  empty: {
    defaultTitle: '暫無資料',
  },

  // ─── ErrorState ──────────────────────────────────────────────────────────
  error: {
    defaultTitle: '載入失敗',
    retry: '重試',
    aiUnavailableAria: 'AI 功能暫不可用',
    aiTitle: 'AI 失敗診斷',
    aiTag: '暫不可用',
    aiDesc: 'LLM 供應商無回應,本次未產生診斷。執行結果與日誌照常記錄,核心 CI/CD 不受影響。',
    confidenceLabel: '信心度 {n}% · {level}',
    confidenceHigh: '高',
    confidenceMedium: '中',
    confidenceLow: '低',
  },

  // ─── ToastHost ───────────────────────────────────────────────────────────
  toast: {
    hostAria: '通知',
    itemAria: '{type} 通知: {title}',
    closeAria: '關閉通知: {title}',
  },
}
