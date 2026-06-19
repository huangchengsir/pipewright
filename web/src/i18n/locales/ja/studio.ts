export default {
  // ─── header ────────────────────────────────────────────────────
  brandTitle: 'Pipewright · ライブラリ',
  brandSubtitle: 'カスタムノードスタジオ (Custom Node Studio)',
  namePlaceholder: 'ノードに名前を付ける…',
  nameAria: 'ノード名',
  cancel: 'キャンセル',
  saving: '保存中…',
  saveToLibrary: 'ライブラリに保存',

  // ─── hero ──────────────────────────────────────────────────────
  heroEyebrow: 'ローコード · 一度定義すれば、どこでも再利用',
  heroTitlePre: 'パラメータ化できる一連のステップを、',
  heroTitleEm: 'ドラッグ',
  heroTitlePost: 'して再利用可能なノードに組み立てます',
  heroDescPre: '左のグループ化されたパレットからブロックを中央のキャンバスにドラッグし、カードを並べ替えます。右側では変数を「昇格」してノード表面のパラメータにします。下部では既存の',
  heroDescPost: 'ノードにリアルタイムでコンパイルされ、バックエンドの変更はゼロ、インスタンスは昇格した数項目のみ設定します。',

  // ─── banners ───────────────────────────────────────────────────
  loadingBanner: '読み込み中…',
  loadFailed: '読み込みに失敗しました',
  loadFailedCode: '読み込みに失敗しました（{code}）',
  saveFailed: '保存に失敗しました',
  saveFailedCode: '保存に失敗しました（{code}）',
  errNameRequired: 'ノード名は空にできません',
  errNeedCommandStep: 'コマンドを生成するステップが少なくとも1つ必要です',
  updatedToast: 'カスタムノード「{name}」を更新しました',
  createdToast: 'カスタムノード「{name}」を作成しました',

  // ─── palette ───────────────────────────────────────────────────
  paletteHintPre: '下のブロックを中央のキャンバスに',
  paletteHintStrong: 'ドラッグ',
  paletteHintPost: 'します（またはクリックで末尾に追加）。',

  // ─── compose canvas ────────────────────────────────────────────
  composeTitle: 'ステップの組み立て · カードをドラッグして並べ替え',
  composeEmpty: 'ここにブロックをドラッグして開始 →',
  moveUp: '上へ移動',
  moveDown: '下へ移動',
  deleteStep: '削除',

  // ─── step field labels ─────────────────────────────────────────
  fieldCommandMultiline: 'コマンド（複数行可）',
  fieldInstallCommand: 'インストールコマンド',
  fieldEchoText: 'エコーテキスト',
  phEchoText: 'ビルド開始…',
  fieldEnvKey: '変数名',
  fieldEnvValue: '値',
  fieldTargetDir: '対象ディレクトリ',
  fieldPathDir: 'PATH に追加するディレクトリ',
  fieldArtifactPath: '成果物パス (glob)',
  fieldSaveAs: '保存先名',
  fieldArchiveFile: 'アーカイブファイル',
  fieldExtractTo: '展開先ディレクトリ',
  fieldCondition: 'shell 条件（不成立なら以降をスキップ、set -e 安全）',
  fieldCommand: 'コマンド',
  fieldRetryCount: '回数',
  fieldDelaySecs: '間隔（秒）',
  fieldTimeoutSecs: 'タイムアウト（秒）',
  fieldSleepSecs: '待機秒数',
  fieldProbeUrl: 'プローブ URL',
  fieldNote: 'メモ（# コメントにコンパイルされ、実行されません）',
  fieldTestCommand: 'テストコマンド',
  fieldReportPath: 'レポートパス (JUnit)',
  fieldMinCoverage: 'カバレッジゲート %',

  // ─── right rail tabs ───────────────────────────────────────────
  tabParams: '昇格パラメータ',
  tabMeta: 'ノード表面',

  // ─── params tab ────────────────────────────────────────────────
  paramsHintPre: 'ステップ内で',
  paramsHintPost: 'として参照します。インスタンス再利用時はこれらだけを設定します。',
  paramsEmpty: 'まだ昇格パラメータがありません。スクリプト全体をハードコードしても構いませんが、昇格してはじめてインスタンスで変更できます。',
  removeParamAria: 'パラメータを削除',
  newParamLabel: '新しいパラメータ',
  phDisplayLabel: '表示ラベル',
  phDefaultValue: 'デフォルト値',
  phOptions: 'カンマ区切りの選択肢、例：20, 18, 22',
  addParam: '＋ パラメータを昇格',

  // ─── param type options ────────────────────────────────────────
  paramTypeText: 'テキスト',
  paramTypeSelect: '列挙',
  paramTypeNumber: '数値',
  paramTypeToggle: 'ブール',

  // ─── meta tab ──────────────────────────────────────────────────
  metaImagePre: '実行イメージ（',
  metaImagePost: 'を含められます）',
  metaIcon: 'アイコン',
  metaCategory: 'カテゴリ',
  phCategory: 'ビルドと成果物',
  metaSummaryPre: '一言の説明（',
  metaSummaryPost: 'を含められます）',
  imgPlaceholder: '例:node:20-alpine',
  summaryPlaceholder: '例:npm でビルドして dist を生成',
  metaHint: 'カテゴリは「ノードを追加」セレクターのどのグループに表示されるかを決めます。説明とアイコンは再利用者が最初に目にするカードです。',
  defaultCategory: 'カスタム',

  // ─── bottom: compiled output ───────────────────────────────────
  compiledTitle: 'コンパイル結果 · templated ノード config（バックエンドはそのまま実行）',
  undeclaredWarn: '⚠ ステップが昇格されていないパラメータを参照しています：{refs}（そのまま保持され、インスタンスでは変更できません）',
  compiledComment: '# templated カスタムノード config —— バックエンドは renderTemplate({open}) 後にコンテナ内で実行',
  compiledEmpty: '（まだステップがありません）',

  // ─── bottom: instance preview ──────────────────────────────────
  previewTitle: 'インスタンスプレビュー · パイプラインにドラッグ後に見えるのはこれだけ',
  unnamedNode: '名称未設定ノード',
  customLabel: 'カスタム',
  previewNote: 'これはまさに n8n の「パラメータの昇格→インスタンスの短いリスト」/ Node-RED Subflow properties のパラダイムです。再利用者は内部スクリプトを理解する必要はなく、公開されたパラメータだけを設定します。',
}
