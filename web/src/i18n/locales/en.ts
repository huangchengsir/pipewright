/**
 * English messages. Mirrors the structure of `zh-CN.ts` (the canonical schema).
 */
import type zhCN from './zh-CN'

const en: typeof zhCN = {
  common: {
    refresh: 'Refresh',
    allArrow: 'All →',
    detailArrow: 'Details →',
  },

  locale: {
    label: 'Language',
    'zh-CN': '简体中文',
    en: 'English',
  },

  nav: {
    dashboard: 'Overview',
    projects: 'Projects',
    runs: 'Runs',
    library: 'Library',
    environments: 'Environments',
    dora: 'DORA Metrics',
    serverStatus: 'Servers',
    containers: 'Containers',
    proxyOverview: 'Certificates',
    anomaly: 'Anomaly Detection',
    notifications: 'Notifications',
    settings: 'Settings',
    logout: 'Log out',
    ariaMain: 'Main navigation',
    ariaBrandHome: 'Pipewright home',
    ariaEnvironments: 'Environment deployment history',
    expand: 'Expand sidebar',
    collapse: 'Collapse sidebar',
  },

  shell: {
    logoutTitle: 'Log out?',
    logoutBody: 'You will need to enter your username and password again to return to the console.',
    logoutLocalTitle: 'Logged out locally',
    logoutLocalDetail: 'The server did not respond, but your local session has been cleared.',
    toThemeLight: 'Switch to light',
    toThemeDark: 'Switch to dark',
    themeLight: '◐ Light',
    themeDark: '◑ Dark',
  },

  login: {
    eyebrow: 'Instance online',
    headlineWelcome: 'Welcome back,',
    headlineEnter: 'enter your',
    headlineWord: 'console',
    headlinePunct: '.',
    lede: 'Self-hosted CI/CD and deployment orchestration — AI pinpoints build failures in seconds.',
    formAria: 'Login form',
    account: 'Username',
    password: 'Password',
    showPassword: 'Show password',
    hidePassword: 'Hide password',
    connecting: 'Connecting…',
    enter: 'Enter console',
    trustSelfHosted: '100% self-hosted',
    trustNoEgress: 'No data egress',
    trustSingleBinary: 'Single binary',
    errUsername: 'Please enter your username',
    errPassword: 'Please enter your password',
    errLockout: 'Too many failed attempts. Please try again later.',
    errCredentials: 'Incorrect username or password',
    errCheckCredentials: 'Please check your username and password',
    errNetwork: 'Cannot reach the server. Check that the backend is running and retry.',
    errGeneric: 'Login failed ({status})',
    errUnknown: 'Unknown error, please retry',
  },

  dashboard: {
    title: 'Overview',
    subtitle: 'CI/CD · Deploy · Ops at a glance',
    newProject: '＋ New project',
    kpiAria: 'Key metrics',
    kpiProjects: 'Projects',
    kpiRuns: 'Total runs',
    kpiSuccess: 'Recent success rate',
    kpiSuccessSuffix: 'last 12',
    kpiRunning: 'In progress',
    kpiServers: 'Servers online',
    doraTitle: 'DORA Metrics',
    doraDeployFreq: 'Deployment Frequency',
    doraLeadTime: 'Lead Time for Changes',
    doraCfr: 'Change Failure Rate',
    doraMttr: 'Mean Time to Restore',
    // English title already carries the canonical name → subtitle hidden.
    subDeployFreq: '',
    subLeadTime: '',
    subCfr: '',
    subMttr: '',
    capDeployments: '{count} deployments in {days} days',
    capLeadTime: 'Median time from commit to deploy',
    capCfr: '{failed}/{total} deployments failed',
    capMttr: 'Median time from failure to recovery',
    doraEmpty:
      'No DORA data yet. Once a few deployments complete, the four engineering-performance metrics will be summarized here.',
  },

  dash: {
    recentRuns: 'Recent runs',
    recentRunsEmpty:
      'No runs yet. Once you connect a project and trigger a pipeline, recent builds/deploys appear here.',
    servers: 'Server health',
    serversEmpty:
      'No servers registered yet. Add target hosts on the Servers page to see live CPU/memory/disk here.',
    online: 'Online',
    offline: 'Offline',
    memory: 'Memory',
    disk: 'Disk',
    load: 'Load',
    unreachable: 'Unreachable',
    environments: 'Environment status',
    envEmpty:
      'No environment deployments yet. Configure branch→environment mapping and deploy to see each environment’s current version here.',
    deployed: 'Deployed',
    notDeployed: 'Not deployed',
    alertsTitle: 'Alerts & AI diagnosis',
    anomalyLink: 'Anomaly →',
    resourceAnomalies: 'Resource anomalies',
    noAnomalies: 'No anomalies · all hosts within thresholds ✓',
    aiDiagLoop: 'AI failure-diagnosis loop',
    noDiagFeedback:
      'No diagnosis feedback yet. Rate failure diagnoses with 👍/👎 and accuracy will accrue here.',
    accuracy: 'Accuracy',
    feedbackTotal: '{n} feedback total',
  },

  runStatus: {
    queued: 'Queued',
    running: 'Running',
    waiting_approval: 'Awaiting approval',
    success: 'Success',
    failed: 'Failed',
    partial_failed: 'Partial failure',
    rolled_back: 'Rolled back',
  },

  time: {
    justNow: 'just now',
    minAgo: '{n} min ago',
    hourAgo: '{n} h ago',
    dayAgo: '{n} d ago',
  },
  timeShort: {
    min: '{n}m',
    hour: '{n}h',
    day: '{n}d',
  },

  metrics: {
    band: {
      elite: 'Elite',
      high: 'High',
      medium: 'Medium',
      low: 'Low',
      none: 'No data',
    },
    duration: {
      seconds: '{n}s',
      minutes: '{n} min',
      hours: '{n} h',
      days: '{n} d',
    },
    freq: {
      perDay: '{n}/day',
      perWeek: '{n}/week',
      perMonth: '{n}/month',
    },
  },
}

export default en
