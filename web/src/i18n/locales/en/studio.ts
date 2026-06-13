export default {
  // ─── header ────────────────────────────────────────────────────
  brandTitle: 'Pipewright · Library',
  brandSubtitle: 'Custom Node Studio',
  namePlaceholder: 'Name this node…',
  nameAria: 'Node name',
  cancel: 'Cancel',
  saving: 'Saving…',
  saveToLibrary: 'Save to Library',

  // ─── hero ──────────────────────────────────────────────────────
  heroEyebrow: 'Low-code · Define once, reuse everywhere',
  heroTitlePre: 'Compose parameterizable steps by ',
  heroTitleEm: 'dragging',
  heroTitlePost: ' into a reusable node',
  heroDescPre: 'Drag blocks from the grouped palette on the left into the center canvas and reorder cards; on the right, “promote” variables into node-surface parameters. The bottom compiles in real time into an existing',
  heroDescPost: 'node — zero backend changes, instances configure only the promoted few.',

  // ─── banners ───────────────────────────────────────────────────
  loadingBanner: 'Loading…',
  loadFailed: 'Failed to load',
  loadFailedCode: 'Failed to load ({code})',
  saveFailed: 'Failed to save',
  saveFailedCode: 'Failed to save ({code})',
  errNameRequired: 'Node name cannot be empty',
  errNeedCommandStep: 'At least one step must produce a command',
  updatedToast: 'Updated custom node “{name}”',
  createdToast: 'Created custom node “{name}”',

  // ─── palette ───────────────────────────────────────────────────
  paletteHintPre: 'Drag blocks from below ',
  paletteHintStrong: 'into',
  paletteHintPost: ' the center canvas (or click to append to the end).',

  // ─── compose canvas ────────────────────────────────────────────
  composeTitle: 'Step composition · Drag cards to reorder',
  composeEmpty: 'Drag blocks here to start →',
  moveUp: 'Move up',
  moveDown: 'Move down',
  deleteStep: 'Delete',

  // ─── step field labels ─────────────────────────────────────────
  fieldCommandMultiline: 'Command (multi-line)',
  fieldInstallCommand: 'Install command',
  fieldEchoText: 'Echo text',
  phEchoText: 'Build starting…',
  fieldEnvKey: 'Variable name',
  fieldEnvValue: 'Value',
  fieldTargetDir: 'Target directory',
  fieldPathDir: 'Directory to append to PATH',
  fieldArtifactPath: 'Artifact path (glob)',
  fieldSaveAs: 'Save as',
  fieldArchiveFile: 'Archive file',
  fieldExtractTo: 'Extract to directory',
  fieldCondition: 'Shell condition (skips the rest if false, set -e safe)',
  fieldCommand: 'Command',
  fieldRetryCount: 'Count',
  fieldDelaySecs: 'Interval (sec)',
  fieldTimeoutSecs: 'Timeout (sec)',
  fieldSleepSecs: 'Wait (sec)',
  fieldProbeUrl: 'Probe URL',
  fieldNote: 'Note (compiles into a # comment, not executed)',
  fieldTestCommand: 'Test command',
  fieldReportPath: 'Report path (JUnit)',
  fieldMinCoverage: 'Coverage gate %',

  // ─── right rail tabs ───────────────────────────────────────────
  tabParams: 'Promoted params',
  tabMeta: 'Node surface',

  // ─── params tab ────────────────────────────────────────────────
  paramsHintPre: 'Referenced in steps as',
  paramsHintPost: '; instances configure only these.',
  paramsEmpty: 'No promoted parameters yet. Hardcoding the whole script is fine; promote a value to make it editable per instance.',
  removeParamAria: 'Remove parameter',
  newParamLabel: 'New parameter',
  phDisplayLabel: 'Display label',
  phDefaultValue: 'Default value',
  phOptions: 'Comma-separated options, e.g. 20, 18, 22',
  addParam: '＋ Promote a parameter',

  // ─── param type options ────────────────────────────────────────
  paramTypeText: 'Text',
  paramTypeSelect: 'Enum',
  paramTypeNumber: 'Number',
  paramTypeToggle: 'Boolean',

  // ─── meta tab ──────────────────────────────────────────────────
  metaImagePre: 'Runtime image (may contain ',
  metaImagePost: ')',
  metaIcon: 'Icon',
  metaCategory: 'Category',
  phCategory: 'Build & Artifacts',
  metaSummaryPre: 'One-line summary (may contain ',
  metaSummaryPost: ')',
  metaHint: 'The category decides which group it shows under in the “Add node” picker; the summary + icon are the card reusers see first.',
  defaultCategory: 'Custom',

  // ─── bottom: compiled output ───────────────────────────────────
  compiledTitle: 'Compiled output · templated node config (backend runs it as-is)',
  undeclaredWarn: '⚠ Steps reference unpromoted parameters: {refs} (kept as-is, not editable per instance)',
  compiledComment: '# templated custom node config — backend runs it in a container after renderTemplate({open})',
  compiledEmpty: '(No steps yet)',

  // ─── bottom: instance preview ──────────────────────────────────
  previewTitle: 'Instance preview · All you see after dragging into a pipeline',
  unnamedNode: 'Unnamed node',
  customLabel: 'Custom',
  previewNote: 'This is exactly the n8n “promote params → short instance list” / Node-RED Subflow properties paradigm: reusers need not understand the inner script, only the exposed parameters.',
}
