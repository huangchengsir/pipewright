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
