export default {
  title: 'DORA Metrics',
  subtitle:
    'Delivery performance aggregated from existing run data · Deployment Frequency / Lead Time / Change Failure Rate / Time to Restore',
  generatedAt: '· Data as of {time}',

  window7d: 'Last 7 days',
  window30d: 'Last 30 days',
  window90d: 'Last 90 days',

  projectLabel: 'Project',
  projectFilterAria: 'Filter by project',
  allProjects: 'All projects',
  windowAria: 'Time window',

  errTitle: 'Failed to load DORA metrics',
  errOffline: 'Cannot reach the server. Check that the backend is running and try again.',
  errLoadStatus: 'Failed to load DORA metrics ({status})',
  errLoadRetry: 'Failed to load DORA metrics. Please try again later.',

  summaryDeployments: 'Deployments in {days} days',
  summarySuccess: 'Succeeded',
  summaryFailed: 'Failed',

  metricDeployFreq: 'Deployment Frequency',
  metricLeadTime: 'Lead Time for Changes',
  metricCfr: 'Change Failure Rate',
  metricMttr: 'Mean Time to Restore',

  capDeployFreq: '{count} successful deployments in {days} days',
  capLeadTime: 'Median commit→production time across {count} successful deployments',
  capLeadTimeEmpty: 'No successful deployments yet to compute lead time',
  capCfr: '{failed} / {total} deployments failed',
  capMttr: 'Median duration across {count} "failure→restore" pairs',
  capMttrEmpty: 'No "failure→restore" pairs in this window',

  noteLead:
    'Methodology: a "deployment" = one run reaching a terminal state; lead time falls back to the enqueue time when commit time is missing. DORA metrics derived from CI run data are an ',
  noteEmphasis: 'approximation',
  noteTrail: ' for reference only, not an SLA basis.',
}
