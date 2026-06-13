export default {
  // ─── top bar / breadcrumb ───────────────────────────────────────
  breadcrumbAria: 'Breadcrumb navigation',
  breadcrumbProjects: 'Projects',
  title: 'Pipeline Configuration',

  // ─── tabs ───────────────────────────────────────────────────────
  tabCanvas: 'Pipeline Canvas',
  tabVars: 'Variables & Cache',
  tabTriggers: 'Trigger Settings',
  tabEnvs: 'Environments & Credentials',
  tabStripAria: 'Pipeline configuration tabs',

  // ─── Pipeline as code (GitOps · FR-8-12) ────────────────────────
  pacTitle: 'Pipeline as code',
  pacOnHint: 'Runs read .pipewright.yml from the run branch in the repo (falling back to this config if the file is missing or invalid).',
  pacOffHint: 'When on, each run reads .pipewright.yml from the repo root on the run branch (falling back to this config if missing or invalid).',
  pacToggleFailed: 'Failed to toggle. Please try again.',

  // ─── PR status checks (commit status writeback · Story 8-9 / FR-8-9) ─
  prStatusTitle: 'PR status checks',
  prStatusOnHint: 'When a run finishes, Pipewright detects the repo platform (GitHub/Gitee) and writes the commit success/failure back as a PR check using the project credential (best-effort; failures never affect the run).',
  prStatusOffHint: 'When on, finishing a run detects the repo platform (GitHub/Gitee) and writes the commit status back as a PR check using the project credential (best-effort).',
  prStatusToggleFailed: 'Failed to toggle. Please try again.',

  // ─── Preview repo config (GitOps · fetch & validate .pipewright.yml at a ref) ───
  pacPreviewBtn: 'Preview repo config',
  pacPreviewTitle: 'Preview repo config',
  pacPreviewSub: 'Fetch and validate the repo file at a branch/tag/commit:',
  pacPreviewCloseAria: 'Close preview',
  pacPreviewCloseBtn: 'Close',
  pacPreviewRefLabel: 'Branch / tag / commit',
  pacPreviewFetch: 'Fetch & validate',
  pacPreviewNotFound: 'No .pipewright.yml found at {ref}; runs will fall back to the pipeline configured here.',
  pacPreviewInvalid: 'The .pipewright.yml at {ref} failed validation; runs will silently fall back to the pipeline configured here:',
  pacPreviewValid: 'The .pipewright.yml at {ref} is valid with {count} stage(s); runs will use it.',
  pacPreviewJobCount: '{count} job(s)',
  pacPreviewConnFailed: 'Connection failed. Check your network and retry.',
  pacPreviewFailed: 'Preview failed ({status}). Please try again.',
  pacPreviewFailedRetry: 'Preview failed. Please try again.',

  // ─── toolbar buttons ────────────────────────────────────────────
  aiGenerate: 'AI Generate Pipeline',
  importYaml: 'Import from YAML',
  templates: 'Templates',
  validate: 'Validate Config',
  closeValidationPanel: 'Close validation panel',
  badgeReady: 'Ready',
  badgeErrors: '{n} errors',
  saving: 'Saving…',
  saveDraft: 'Save Draft',

  // ─── banners / status ───────────────────────────────────────────
  dismiss: 'Dismiss',
  draftSaved: 'Pipeline draft saved',
  retry: 'Retry',
  loading: 'Loading',

  // ─── load errors ────────────────────────────────────────────────
  errNoServer: 'Cannot reach the server. Check that the backend is running and try again.',
  errProjectNotFound: 'Project not found. Please verify the project ID.',
  errLoadFailedStatus: 'Failed to load pipeline ({status})',
  errLoadFailedRetry: 'Failed to load pipeline. Please try again later.',

  // ─── save errors ────────────────────────────────────────────────
  errSaveFailedRetry: 'Save failed. Please try again later.',
  errSaveFailedStatus: 'Save failed ({status})',
  errInvalidStage: 'Stage name cannot be empty and kind must be an allowed value. Please check and try again.',
  errInvalidJob: 'Job name or type cannot be empty. Please complete them and try again.',
  errDuplicateId: 'Duplicate stage or job ID. Please remove duplicates and try again.',
  errInvalidBuild: 'Build model must be dockerfile/toolchain, and artifact type must be image/jar/dist.',
  errInvalidVar: 'Variable key cannot be empty and must be unique within its scope; secret variables require a vault credential.',
  errInvalidEnvironment: 'Environment name cannot be empty, and image registry type must be harbor/acr/dockerhub/custom.',
  errCredentialNotFound: 'The referenced vault credential does not exist. Please reselect and try again.',
  errVaultUnconfigured: 'The vault has no master key configured, so secret credentials cannot be referenced.',
}
