export default {
  title: 'Containers',
  subtitle: 'Manage containers and images per host across all registered servers',
  countSummary: '· {total} containers total · {running} running',
  autoRefresh: '· Auto-refreshes every {n}s',

  aiAssistant: '✦ AI Assistant',
  prune: '🧹 Prune',
  bulkEnter: 'Bulk',
  bulkExit: 'Exit bulk',
  create: '+ New container',

  loadingAria: 'Loading container list',
  errTitle: 'Failed to load container list',
  errConnect: 'Cannot connect to the server. Check that the backend is running and try again.',
  errLoadStatus: 'Failed to load container list ({status})',
  errLoadRetry: 'Failed to load container list. Please try again later.',

  emptyTitle: 'No registered servers yet',
  emptyDesc: 'Register target servers under "Settings › Servers" and their containers and images will be aggregated here.',

  kpiTotal: 'Total containers',
  kpiRunning: 'Running',
  kpiStopped: 'Stopped',
  kpiHosts: 'Servers with containers',
  kpiStripAria: 'Container aggregate stats',

  filterAria: 'Filter containers by state',
  filterAll: 'All',
  filterRunning: 'Running',
  filterStopped: 'Stopped',
  filterPaused: 'Paused',

  searchPlaceholder: 'Search containers by name / image',
  searchAria: 'Search containers by name or image',
  searchClear: 'Clear search',

  bulkAria: 'Bulk actions',
  bulkSelected: 'Selected',
  bulkSelectedUnit: '',
  bulkClear: 'Clear selection',
  actionStart: 'Start',
  actionStop: 'Stop',
  actionRestart: 'Restart',
  actionDelete: 'Delete',

  confirmTitle: 'Bulk {label} {n} containers?',
  confirmBodyRm: 'The selected containers will be deleted (docker rm). Running containers must be stopped first, otherwise deletion fails (counted as failures).',
  confirmBodyAction: 'The {label} action will run on the {n} selected containers; related services may be briefly interrupted.',
  confirmLabel: '{label} {n}',

  toastDone: 'Bulk {label} complete',
  toastDoneDetail: '{n} succeeded',
  toastFail: 'Bulk {label} failed',
  toastFailDetail: '{n} failed',
  toastPartial: 'Bulk {label} partially complete',
  toastPartialDetail: '{ok} succeeded · {fail} failed',

  cardsAria: 'Per-server container cards',
  aiContextContainer: '(docker host)',
}
