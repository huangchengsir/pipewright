export default {
  // ─── header ────────────────────────────────────────────────────
  brandTitle: 'Pipewright · 複用庫',
  brandSubtitle: '自訂節點工作室 (Custom Node Studio)',
  namePlaceholder: '為節點取個名字…',
  nameAria: '節點名稱',
  cancel: '取消',
  saving: '儲存中…',
  saveToLibrary: '儲存到複用庫',

  // ─── hero ──────────────────────────────────────────────────────
  heroEyebrow: '低程式碼 · 一處定義,處處複用',
  heroTitlePre: '把一段可參數化的步驟,',
  heroTitleEm: '拖曳',
  heroTitlePost: '組合成可複用節點',
  heroDescPre: '左側積木庫按分組拖進中間畫布、拖動卡片排序;右側把變數「提升」成節點表面參數。底部即時編譯成既有的',
  heroDescPost: '節點 —— 後端零改,實例只設定提升的那幾項。',

  // ─── banners ───────────────────────────────────────────────────
  loadingBanner: '載入中…',
  loadFailed: '載入失敗',
  loadFailedCode: '載入失敗({code})',
  saveFailed: '儲存失敗',
  saveFailedCode: '儲存失敗({code})',
  errNameRequired: '節點名稱不能為空',
  errNeedCommandStep: '至少要有一個會產生命令的步驟',
  updatedToast: '已更新自訂節點「{name}」',
  createdToast: '已建立自訂節點「{name}」',

  // ─── palette ───────────────────────────────────────────────────
  paletteHintPre: '從下面把積木',
  paletteHintStrong: '拖進',
  paletteHintPost: '中間畫布(或點一下追加到末尾)。',

  // ─── compose canvas ────────────────────────────────────────────
  composeTitle: '步驟組合 · 拖動卡片可重排',
  composeEmpty: '把左側積木拖到這裡開始 →',
  moveUp: '上移',
  moveDown: '下移',
  deleteStep: '刪除',

  // ─── step field labels ─────────────────────────────────────────
  fieldCommandMultiline: '命令(可多行)',
  fieldInstallCommand: '安裝命令',
  fieldEchoText: '回顯文字',
  phEchoText: '建置開始…',
  fieldEnvKey: '變數名',
  fieldEnvValue: '值',
  fieldTargetDir: '目標目錄',
  fieldPathDir: '追加到 PATH 的目錄',
  fieldArtifactPath: '產物路徑 (glob)',
  fieldSaveAs: '另存為',
  fieldArchiveFile: '歸檔檔案',
  fieldExtractTo: '解壓到目錄',
  fieldCondition: 'shell 條件(不成立則跳過後續,set -e 安全)',
  fieldCommand: '命令',
  fieldRetryCount: '次數',
  fieldDelaySecs: '間隔秒',
  fieldTimeoutSecs: '逾時秒',
  fieldSleepSecs: '等待秒數',
  fieldProbeUrl: '探測 URL',
  fieldNote: '備註(編譯成 # 註解,不執行)',
  fieldTestCommand: '測試命令',
  fieldReportPath: '報告路徑 (JUnit)',
  fieldMinCoverage: '覆蓋率門檻 %',

  // ─── right rail tabs ───────────────────────────────────────────
  tabParams: '提升參數',
  tabMeta: '節點表面',

  // ─── params tab ────────────────────────────────────────────────
  paramsHintPre: '步驟裡以',
  paramsHintPost: '引用;實例複用時只設定這幾項。',
  paramsEmpty: '還沒有提升參數。整段腳本寫死也行,提升後才可在實例裡改。',
  removeParamAria: '移除參數',
  newParamLabel: '新參數',
  phDisplayLabel: '顯示標籤',
  phDefaultValue: '預設值',
  phOptions: '逗號分隔選項,如 20, 18, 22',
  addParam: '＋ 提升一個參數',

  // ─── param type options ────────────────────────────────────────
  paramTypeText: '文字',
  paramTypeSelect: '列舉',
  paramTypeNumber: '數字',
  paramTypeToggle: '布林',

  // ─── meta tab ──────────────────────────────────────────────────
  metaImagePre: '執行映像(可含 ',
  metaImagePost: ')',
  metaIcon: '圖示',
  metaCategory: '分類',
  phCategory: '建置與製品',
  metaSummaryPre: '一句話說明(可含 ',
  metaSummaryPost: ')',
  imgPlaceholder: '例:node:20-alpine',
  summaryPlaceholder: '例:用 npm 建置並產出 dist',
  metaHint: '分類決定它出現在「加節點」選擇器的哪一組;說明 + 圖示是複用者第一眼看到的卡片。',
  defaultCategory: '自訂',

  // ─── bottom: compiled output ───────────────────────────────────
  compiledTitle: '編譯產出 · templated 節點 config(後端原樣執行)',
  undeclaredWarn: '⚠ 步驟引用了未提升的參數:{refs}(將原樣保留,實例改不動)',
  compiledComment: '# templated 自訂節點 config —— 後端 renderTemplate({open}) 後在容器內執行',
  compiledEmpty: '(還沒有任何步驟)',

  // ─── bottom: instance preview ──────────────────────────────────
  previewTitle: '實例預覽 · 拖進流水線後只見這些',
  unnamedNode: '未命名節點',
  customLabel: '自訂',
  previewNote: '這正是 n8n「promote 參數→實例短清單」/ Node-RED Subflow properties 的範式:複用者不必懂內部腳本,只設定暴露出來的參數。',
}
