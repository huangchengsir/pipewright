export default {
  title: 'Server Status',
  subtitle: 'CPU load, memory and disk usage for all registered servers, collected live over SSH',
  reachableSummary: '{reachable}/{total} reachable',
  autoRefresh: 'Auto-refreshes every {n}s',
  loadingAria: 'Loading server status',
  errTitle: 'Failed to load server status',
  errConnect: 'Cannot connect to the server. Check that the backend is running and try again.',
  errLoadStatus: 'Failed to load server status ({status})',
  errLoadRetry: 'Failed to load server status. Please try again later.',
  emptyTitle: 'No registered servers yet',
  emptyDesc: 'Register target servers under "Settings › Servers" and their resource metrics will appear here.',
}
