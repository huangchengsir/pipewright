export default {
  // ─── page header ───────────────────────────────────────────────
  title: 'Projects',
  subtitle: 'Managed Gitee repositories — each project maps to a pipeline configuration and deploy targets',
  newProject: 'New project',
  retry: 'Retry',

  // ─── toolbar / search / filter ─────────────────────────────────
  searchFilterAria: 'Search and filter projects',
  searchPlaceholder: 'Search by name, repository URL, or branch…',
  searchAria: 'Search projects',
  statusFilterAria: 'Filter by status',
  statusAll: 'All statuses',

  // ─── list states ───────────────────────────────────────────────
  loading: 'Loading',
  emptyTitle: 'No projects yet',
  emptyHint: 'Connect your first Gitee repository, then configure a pipeline and deploy to target servers.',
  noMatchTitle: 'No matching projects',
  noMatchHint: 'Adjust your search term or status filter and try again.',
  clearFilter: 'Clear filters',
  resultCount: '{n} projects',
  resultCountTotal: '(of {total})',

  // ─── project card ──────────────────────────────────────────────
  runStatusAria: 'Run status: {status}',
  lastRun: 'Last run',
  noRun: 'No runs yet',
  targetServers: 'Target servers',
  notBound: 'Not bound',
  credential: 'Repository credential',
  credentialRefTitle: 'Credential reference (not plaintext)',
  updatedAt: 'Updated {time}',

  // ─── card actions ──────────────────────────────────────────────
  actionRunTitle: 'Trigger run manually · {name}',
  actionRunAria: 'Manually trigger a pipeline run for project {name}',
  actionRenameTitle: 'Rename {name}',
  actionRenameAria: 'Rename project {name}',
  actionCodeTitle: 'Browse code · {name}',
  actionCodeAria: 'Browse code for project {name}',
  actionPipelineTitle: 'Pipeline configuration · {name}',
  actionPipelineAria: 'Configure pipeline for project {name}',
  actionDeleteTitle: 'Delete {name}',
  actionDeleteAria: 'Delete project {name}',

  // ─── shared modal ──────────────────────────────────────────────
  closeDialog: 'Close dialog',
  cancel: 'Cancel',
  save: 'Save',
  saving: 'Saving…',

  // ─── trigger modal ─────────────────────────────────────────────
  triggerDialogAria: 'Manual trigger · {name}',
  triggerTitle: 'Trigger run manually',
  triggerSub: '{name} · pick a branch and create a pipeline run right away',
  branch: 'Branch',
  branchHint: '(optional, leave empty to use the project default branch)',
  commit: 'Commit',
  commitHint: '(optional, leave empty to use the branch HEAD)',
  commitPlaceholder: 'e.g. a3f1c2d',
  params: 'Parameters',
  paramsHintTyped: '(fill in per definition, injected into the pipeline as environment variables)',
  paramsHintFree: '(optional, injected into the pipeline as environment variables)',
  triggering: 'Triggering…',
  runNow: 'Run now',

  // ─── create modal ──────────────────────────────────────────────
  createSub: 'Connect a Gitee repository and bind a repository credential',
  fieldName: 'Project name',
  fieldNamePlaceholder: 'e.g. acme-web',
  fieldRepo: 'Repository URL',
  fieldCredHint: '(only Git token credentials are shown, never plaintext)',
  credLoading: 'Loading credentials…',
  credSelect: 'Select a Git token credential',
  credEmptyPre: 'No Git token credentials yet. Go to the',
  credVaultLink: 'credential vault',
  credEmptyPost: 'to add one.',
  fieldDefaultBranch: 'Default branch',
  fieldDefaultBranchHint: '(optional, leave empty to auto-detect via test connection)',
  testConnection: 'Test connection',
  testing: 'Testing…',
  testOk: 'Connection successful',
  testDetectedBranch: '· default branch {branch}',
  creating: 'Creating…',
  createSubmit: 'Create project',

  // ─── rename modal ──────────────────────────────────────────────
  renameTitle: 'Rename project',
  renameSub: 'Change the display name of the project',

  // ─── delete modal ──────────────────────────────────────────────
  deleteDialogAria: 'Confirm project deletion',
  deleteTitle: 'Delete project',
  deleteSub: 'This action cannot be undone',
  deleteConfirmPre: 'Are you sure you want to permanently delete project',
  deleteConfirmPost: '? Its pipeline configuration, run history, and credential references will all be cleaned up.',
  deleting: 'Deleting…',
  confirmDelete: 'Confirm delete',

  // ─── error messages ────────────────────────────────────────────
  errNetwork: 'Cannot reach the server. Check that the backend is running and retry.',
  errNetworkRetry: 'Cannot reach the server. Please try again later.',
  errLoadNetwork: 'Cannot reach the server. Check that the backend is running and retry',
  errLoadStatus: 'Failed to load projects ({status})',
  errLoadRetry: 'Failed to load projects, please try again later',
  errNameRequired: 'Please enter a project name',
  errRepoRequired: 'Please enter a repository URL',
  errRepoFormat: 'Invalid repository URL format. It must start with https:// or git{\'@\'}',
  errCredRequired: 'Please select a repository credential',
  errRepoFirst: 'Please enter a repository URL first',
  errCredFirst: 'Please select a repository credential first',
  errNameEmpty: 'Project name cannot be empty',

  testErrCredential: 'Credential error: check that your Gitee access token is valid and update it in the credential vault.',
  testErrUnreachable: 'Repository unreachable: confirm the URL is correct and the repository exists and is accessible.',
  testErrVault: 'The vault has no master key configured, so credentials cannot be read.',
  testErrStatus: 'Connection test failed ({status})',
  testErrRetry: 'Connection test failed, please try again later.',

  createErrCredField: 'Credential error: check that the access token is valid',
  createErrCredBanner: 'Credential validation failed. Switch credentials or update it in the credential vault.',
  createErrRepoField: 'Repository unreachable: confirm the URL is correct and accessible',
  createErrRepoBanner: 'Repository URL unreachable, creation failed.',
  createErrVault: 'The vault has no master key configured, so the project cannot be saved.',
  createErrStatus: 'Creation failed ({status})',
  createErrRetry: 'Creation failed, please try again later.',

  renameErrStatus: 'Rename failed ({status})',
  renameErrRetry: 'Rename failed, please try again later.',

  deleteErrStatus: 'Delete failed ({status})',
  deleteErrRetry: 'Delete failed, please try again later.',

  triggerErrNotFound: 'Project not found, please refresh and retry.',
  triggerErrStatus: 'Trigger failed ({status})',
  triggerErrRetry: 'Trigger failed, please try again later.',
}
