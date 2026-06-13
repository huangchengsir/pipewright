export default {
  // ─── shared ──────────────────────────────────────────────────────────────
  loadingShort: 'Loading…',

  // ─── AuditTimeline ───────────────────────────────────────────────────────
  audit: {
    title: 'Audit Log',
    sub: 'Who · When · What',
    treeAria: 'Audit timeline',
    emptyLabel: 'No audit records yet',
    emptyHint: 'Sensitive operations such as creating, updating or deleting credentials and projects, resetting webhook secrets, and triggering runs manually are all recorded here. Records are tamper-proof.',
    loadMore: 'Load more audit records →',
    via: 'Web console',
    actorYou: 'You',
    verbCreate: 'Created',
    verbUpdate: 'Updated',
    verbDelete: 'Deleted',
    verbReset: 'Reset',
    verbAdd: 'Connected',
    verbTrigger: 'Manually triggered',
    verbDefault: 'Acted on',
    nounCredential: 'credential',
    nounWebhookSecret: 'webhook signing secret',
    nounProject: 'project',
    nounRun: 'run',
    errConnect: 'Cannot connect to the server. Check that the backend is running and try again.',
    errLoad: 'Failed to load audit log ({status})',
    errLoadRetry: 'Failed to load audit log. Please try again later.',
  },

  // ─── CodeTree / CodeTreeNode ─────────────────────────────────────────────
  tree: {
    aria: 'Code directory tree',
    fileAria: 'Repository file tree',
    title: 'Files',
    refTitle: 'Current ref: {ref}',
    loadingDir: 'Loading directory…',
    emptyRepo: 'Empty repository / source not readable',
    emptyDir: 'Empty directory',
    errConnect: 'Cannot connect to the server',
    errNotFound: 'Path not found',
    errLoad: 'Load failed ({status})',
    errLoadGeneric: 'Load failed',
  },

  // ─── CodeViewer ──────────────────────────────────────────────────────────
  code: {
    viewAria: 'Code view',
    editorAria: 'Code editor (read-only)',
    noFileSelected: 'No file selected',
    truncated: 'Truncated',
    truncatedTitle: 'File too large; only the leading portion is shown',
    idleTitle: 'Select a file on the left to view',
    idleSub: 'Read-only browse of repository source with syntax highlighting; editing and committing are not available.',
    binaryTitle: 'Binary file, cannot be previewed',
    degradedTitle: 'Source not readable',
    degradedSub: 'Repository clone failed or the current environment cannot access it. Please retry later or check the project repository settings.',
    errTitle: 'Failed to load file',
    fallbackRegionAria: 'Code content (plain-text fallback)',
    fallbackNote: 'Syntax highlighter failed to load; fell back to a plain-text view.',
    errConnect: 'Cannot connect to the server',
    errNotFound: 'File not found',
    errLoad: 'Load failed ({status})',
    errLoadGeneric: 'Load failed',
  },

  // ─── ConfirmDialog ───────────────────────────────────────────────────────
  confirm: {
    cancel: 'Cancel',
    confirm: 'Confirm',
    typeLabelPrefix: 'Type',
    typeLabelSuffix: 'to confirm',
    typePlaceholder: 'Type {text}…',
    typeAria: 'Type {text} to confirm the action',
  },

  // ─── EmptyState ──────────────────────────────────────────────────────────
  empty: {
    defaultTitle: 'No data',
  },

  // ─── ErrorState ──────────────────────────────────────────────────────────
  error: {
    defaultTitle: 'Failed to load',
    retry: 'Retry',
    aiUnavailableAria: 'AI feature unavailable',
    aiTitle: 'AI failure diagnosis',
    aiTag: 'Unavailable',
    aiDesc: 'The LLM provider did not respond, so no diagnosis was generated this time. Run results and logs are recorded as usual; core CI/CD is unaffected.',
    confidenceLabel: 'Confidence {n}% · {level}',
    confidenceHigh: 'High',
    confidenceMedium: 'Medium',
    confidenceLow: 'Low',
  },

  // ─── ToastHost ───────────────────────────────────────────────────────────
  toast: {
    hostAria: 'Notifications',
    itemAria: '{type} notification: {title}',
    closeAria: 'Close notification: {title}',
  },
}
