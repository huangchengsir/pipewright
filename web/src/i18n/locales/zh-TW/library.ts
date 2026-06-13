export default {
  // ─── page header ───────────────────────────────────────────────
  title: '複用庫',
  subtitle: '跨專案共享的流水線範本、變數組與自訂節點 · 一處定義,處處複用',
  newGroup: '+ 建立變數組',
  newStudioNode: '+ 建立工作室節點',

  // ─── segmented tabs ────────────────────────────────────────────
  segAria: '複用庫分類',
  tabTemplates: '流水線範本',
  tabVariableGroups: '變數組',
  tabCustomNodes: '自訂節點',

  // ─── common ────────────────────────────────────────────────────
  retry: '重試',
  delete: '刪除',
  edit: '編輯',
  cancel: '取消',
  save: '儲存',
  saving: '儲存中…',
  close: '關閉',
  remove: '移除',
  noDescription: '無說明',
  emptyValue: '空',
  updatedAt: '更新於 {time}',

  // ─── templates ─────────────────────────────────────────────────
  emptyTemplatesTitle: '還沒有流水線範本',
  emptyTemplatesHint: '範本讓你把一份流水線定義沉澱下來,在任意專案的流水線編輯器裡一鍵套用。在專案流水線編輯器中可把目前流水線另存為範本。',
  stageCount: '{n} 階段',

  // ─── variable groups ───────────────────────────────────────────
  emptyGroupsTitle: '還沒有變數組',
  emptyGroupsHint: '把一組共享變數(如同一套環境位址、權杖引用)定義為變數組,在多條流水線間複用。secret 變數只儲存保險庫引用,絕不儲存明文。',
  varCount: '{n} 變數',
  secretRefTitle: '保險庫引用,明文不可見',
  moreVars: '+{n} 個變數…',
  noVars: '無變數',

  // ─── custom nodes ──────────────────────────────────────────────
  emptyNodesTitle: '還沒有自訂節點',
  emptyNodesHint: '點右上「建立工作室節點」以低程式碼方式組合步驟 + 提升參數,沉澱成可複用節點;或在流水線編輯器裡把任意節點設好參數後點「另存為自訂節點」。之後在任意流水線的節點選擇器一鍵複用。',
  moreParams: '+{n} 個參數…',
  noParams: '無參數',

  // ─── variable-group editor ─────────────────────────────────────
  createGroup: '建立變數組',
  editGroup: '編輯變數組',
  fieldName: '名稱',
  fieldDescriptionOptional: '說明(選填)',
  fieldVariables: '變數',
  addVariable: '+ 新增變數',
  groupNamePlaceholder: '如 prod-shared-env',
  groupDescPlaceholder: '這組變數的用途',
  selectCredential: '選擇憑證…',
  secretToggleOn: '保險庫 secret(點擊切回明文)',
  secretToggleOff: '明文(點擊切為 secret)',

  // ─── custom-node editor ────────────────────────────────────────
  editNode: '編輯自訂節點',
  fieldSummaryOptional: '摘要(選填)',
  fieldUnderlyingType: '底層類型',
  underlyingTypeHint: '底層任務類型不可變更',
  fieldParams: '參數',
  addParam: '+ 新增參數',
  nodeNamePlaceholder: '如 build-and-push',
  nodeDescPlaceholder: '這個節點的用途',
  nodeSummaryPlaceholder: '一句話摘要,顯示在卡片上',
  noParamsHint: '暫無參數,點「+ 新增參數」新增。',

  // ─── confirms / toasts / errors ────────────────────────────────
  confirmDeleteTemplate: '刪除範本「{name}」?此操作不可復原。',
  deletedTemplate: '已刪除範本「{name}」',
  confirmDeleteGroup: '刪除變數組「{name}」?此操作不可復原。',
  deletedGroup: '已刪除變數組「{name}」',
  createdGroup: '已建立變數組「{name}」',
  updatedGroup: '已更新變數組「{name}」',
  confirmDeleteNode: '刪除自訂節點「{name}」?此操作不可復原。',
  deletedNode: '已刪除自訂節點「{name}」',
  updatedNode: '已更新自訂節點「{name}」',
  groupNameRequired: '變數組名稱不能為空',
  nodeNameRequired: '自訂節點名稱不能為空',
  deleteFailed: '刪除失敗',
  saveFailed: '儲存失敗',
  saveFailedStatus: '儲存失敗({status})',
  loadTemplatesFailed: '載入範本失敗',
  loadTemplatesFailedStatus: '載入範本失敗({status})',
  loadGroupsFailed: '載入變數組失敗',
  loadGroupsFailedStatus: '載入變數組失敗({status})',
  loadNodesFailed: '載入自訂節點失敗',
  loadNodesFailedStatus: '載入自訂節點失敗({status})',
}
