export default {
  title: 'Environments',
  subtitle:
    'Deployment history grouped by environment and the current active version · roll back to the last successful deploy in one click',
  project: 'Project',
  filterByProject: 'Filter by project',
  selectProject: 'Select a project',

  emptySelectTitle: 'Select a project',
  emptySelectDesc: 'Deployment history is grouped by project — pick one above first.',
  emptyTitle: 'No deployment history for this project yet',
  emptyDesc:
    'Once a deploy has run against an environment (the webhook branch mapping resolves the environment name and the deploy completes), the timeline will appear here grouped by environment.',

  errLoadTitle: 'Failed to load deployment history',
  errNetwork: 'Cannot reach the server. Check that the backend is running and try again.',
  errLoad: 'Failed to load deployment history ({status})',
  errLoadRetry: 'Failed to load deployment history. Please try again later.',

  active: 'Active',
  activeVersionTitle: 'Current active version',
  noActiveVersion: 'No active version',
  noFullSuccessTitle: 'No fully successful deploy yet',
  targetCount: '{n} target hosts',

  rollback: 'Roll back',
  rollbackEnabledTitle: 'Roll back to the last successful deploy',
  rollbackDisabledTitle: 'No previous successful deploy to roll back to',
  rollbackTitle: 'Roll back environment "{env}"',
  rollbackBody:
    'This rolls the environment back to the last successful deploy (run {commit} · {when}), redeploying those artifacts to the original target hosts. This triggers a real deployment.',
  rollbackConfirm: 'Confirm rollback',
  rollbackFailedStatus: 'Rollback failed ({status})',
  rollbackFailedRetry: 'Rollback failed. Please try again later.',

  toastRolledBack: 'Environment "{env}" rolled back',
  toastRolledBackDetail: 'Artifacts redeployed to {n} target hosts',
  toastRollbackPartial: 'Environment "{env}" partially failed to roll back',
  toastRollbackPartialDetail: '{failed}/{total} target hosts failed',
  toastRollbackFailed: 'Environment "{env}" rollback failed',

  timelineAria: '{env} deployment history',
}
