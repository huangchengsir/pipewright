export default {
  // ─── page header ───────────────────────────────────────────────
  title: 'ライブラリ',
  subtitle: 'プロジェクト横断で共有するパイプラインテンプレート・変数グループ・カスタムノード · 一度定義すればどこでも再利用',
  newGroup: '+ 変数グループを作成',
  newStudioNode: '+ スタジオノードを作成',

  // ─── segmented tabs ────────────────────────────────────────────
  segAria: 'ライブラリの分類',
  tabTemplates: 'パイプラインテンプレート',
  tabVariableGroups: '変数グループ',
  tabCustomNodes: 'カスタムノード',

  // ─── common ────────────────────────────────────────────────────
  retry: '再試行',
  delete: '削除',
  edit: '編集',
  cancel: 'キャンセル',
  save: '保存',
  saving: '保存中…',
  close: '閉じる',
  remove: '削除',
  noDescription: '説明なし',
  emptyValue: '空',
  updatedAt: '更新日 {time}',

  // ─── templates ─────────────────────────────────────────────────
  emptyTemplatesTitle: 'パイプラインテンプレートがまだありません',
  emptyTemplatesHint: 'テンプレートを使うとパイプライン定義を保存し、任意のプロジェクトのパイプラインエディタでワンクリックで適用できます。プロジェクトのパイプラインエディタで現在のパイプラインをテンプレートとして保存できます。',
  stageCount: '{n} ステージ',

  // ─── variable groups ───────────────────────────────────────────
  emptyGroupsTitle: '変数グループがまだありません',
  emptyGroupsHint: '共有変数のセット(同じ環境アドレスやトークン参照など)を変数グループとして定義し、複数のパイプライン間で再利用できます。secret 変数は vault 参照のみを保存し、平文は決して保存しません。',
  varCount: '{n} 個の変数',
  secretRefTitle: 'vault 参照、平文は表示されません',
  moreVars: '他 {n} 個の変数…',
  noVars: '変数なし',

  // ─── custom nodes ──────────────────────────────────────────────
  emptyNodesTitle: 'カスタムノードがまだありません',
  emptyNodesHint: '右上の「スタジオノードを作成」をクリックし、ローコードでステップを組み合わせてパラメータを昇格させ、再利用可能なノードとして保存します。またはパイプラインエディタで任意のノードのパラメータを設定してから「カスタムノードとして保存」をクリックします。その後、任意のパイプラインのノードセレクタからワンクリックで再利用できます。',
  moreParams: '他 {n} 個のパラメータ…',
  noParams: 'パラメータなし',

  // ─── variable-group editor ─────────────────────────────────────
  createGroup: '変数グループを作成',
  editGroup: '変数グループを編集',
  fieldName: '名前',
  fieldDescriptionOptional: '説明(任意)',
  fieldVariables: '変数',
  addVariable: '+ 変数を追加',
  groupNamePlaceholder: '例: prod-shared-env',
  groupDescPlaceholder: 'この変数グループの用途',
  selectCredential: '認証情報を選択…',
  secretToggleOn: 'vault secret(クリックで平文に戻す)',
  secretToggleOff: '平文(クリックで secret に切り替え)',

  // ─── custom-node editor ────────────────────────────────────────
  editNode: 'カスタムノードを編集',
  fieldSummaryOptional: '概要(任意)',
  fieldUnderlyingType: '基盤タイプ',
  underlyingTypeHint: '基盤となるジョブタイプは変更できません',
  fieldParams: 'パラメータ',
  addParam: '+ パラメータを追加',
  nodeNamePlaceholder: '例: build-and-push',
  nodeDescPlaceholder: 'このノードの用途',
  nodeSummaryPlaceholder: 'カードに表示する一行の概要',
  noParamsHint: 'パラメータはまだありません。「+ パラメータを追加」をクリックして追加してください。',

  // ─── confirms / toasts / errors ────────────────────────────────
  confirmDeleteTemplate: 'テンプレート「{name}」を削除しますか?この操作は元に戻せません。',
  deletedTemplate: 'テンプレート「{name}」を削除しました',
  confirmDeleteGroup: '変数グループ「{name}」を削除しますか?この操作は元に戻せません。',
  deletedGroup: '変数グループ「{name}」を削除しました',
  createdGroup: '変数グループ「{name}」を作成しました',
  updatedGroup: '変数グループ「{name}」を更新しました',
  confirmDeleteNode: 'カスタムノード「{name}」を削除しますか?この操作は元に戻せません。',
  deletedNode: 'カスタムノード「{name}」を削除しました',
  updatedNode: 'カスタムノード「{name}」を更新しました',
  groupNameRequired: '変数グループ名は空にできません',
  nodeNameRequired: 'カスタムノード名は空にできません',
  deleteFailed: '削除に失敗しました',
  saveFailed: '保存に失敗しました',
  saveFailedStatus: '保存に失敗しました({status})',
  loadTemplatesFailed: 'テンプレートの読み込みに失敗しました',
  loadTemplatesFailedStatus: 'テンプレートの読み込みに失敗しました({status})',
  loadGroupsFailed: '変数グループの読み込みに失敗しました',
  loadGroupsFailedStatus: '変数グループの読み込みに失敗しました({status})',
  loadNodesFailed: 'カスタムノードの読み込みに失敗しました',
  loadNodesFailedStatus: 'カスタムノードの読み込みに失敗しました({status})',
}
