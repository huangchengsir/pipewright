export default {
  aiTitle: 'AI interpretation',
  boundaryDetails: 'Report boundaries and evidence retention',
  titles: {
    notFound: 'Container confirmed absent at sampling time',
    unknown: 'This evidence cannot be interpreted yet',
    unavailable: 'No valid runtime sample obtained',
    running: 'Container running at sampling time',
    exited: 'Container not running at sampling time',
    oom: 'Sample records an out-of-memory termination',
    image: 'Expected image identity recorded',
    gateFailed: 'Deployment-time health gate failed',
    logs: 'Bounded windowed logs retained',
    unhealthy: 'Docker health check failed at sampling time',
    logsUnavailable: 'No windowed logs obtained',
    logsPartial: 'Only partial windowed logs retained',
    logsEmpty: 'Log window collected; no lines returned',
  },
  logExcerpt:
    'Log excerpt (up to 600 characters; complete content in technical details)',
  snapshotScope:
    'Interprets only the snapshot selected and approved for each send. It does not analyze the full report or investigate automatically.',
  nextTitle: 'What to do next',
  nextDescription:
    'Review failure evidence before investigating changes or repeating observation.',
  investigateAction: 'Investigate related changes',
  why: 'Why verification failed',
  failedFallback:
    'The rules confirm a failure. Detailed conditions are unavailable; review the technical data.',
  unknownTitle: 'Still unconfirmed',
  unknownScope:
    'Samples do not establish continuous availability or business health. A complete root cause requires independent evidence.',
  unconfirmed:
    'Unconfirmed information; the original record is retained in technical details.',
  unknown: 'Unknown',
  unknownTarget: 'Unconfirmed target',
  unknownCondition: 'Unrecognized check condition',
  reportMissing: 'This release guard report was not found.',
  technicalDetails: 'Technical details',
  reportDetails: 'Original report data',
  evidenceReference: 'View evidence {n}',
  evidenceLoaded: '{n} loaded; this count covers only loaded evidence.',
  continueFinding: 'Continue finding this evidence',
  find: {
    found: 'The cited evidence was found.',
    pending: 'Evidence is not loaded yet. Continue one page at a time.',
    loading: 'Reading and locating evidence…',
    missing: 'The last page has been reached. This evidence was not found.',
    expired:
      'Evidence has expired and cannot be located. The historical conclusion is retained.',
    error: 'Evidence could not be read. Continue the search manually.',
    stalled:
      'The page cursor did not advance. Searching stopped; refresh the report before trying again.',
  },
  kinds: {
    runtime: 'Runtime sample',
    baseline: 'Predeployment baseline',
    expected_image: 'Expected image record',
    deployment_health_gate: 'Deployment-time health gate',
    logs: 'Windowed logs',
    unknown: 'Unrecognized evidence type',
  },
  running: 'Running state',
  runningValue: 'Running',
  stoppedValue: 'Not running',
  exitCode: 'Exit code',
  oom: 'Out-of-memory termination',
  restarts: 'Restart count',
  yes: 'Yes',
  no: 'No',
  health: 'Docker health state',
  healthValues: {
    healthy: 'Docker check passed',
    unhealthy: 'Docker check failed',
    starting: 'Docker check starting',
    absent: 'Not configured; business health unknown',
    unknown: 'Unknown',
  },
  imageIdentity: 'Image identity',
  imageRecorded: 'Identity value',
  recorded: 'Recorded; see technical details',
  identityValues: {
    config_id: 'Local Docker Image ID',
    manifest: 'Repository manifest digest',
    index: 'Repository index digest',
    repository_digest_unknown: 'Repository digest type unknown',
    unknown: 'Unknown',
  },
  gate: 'Deployment gate',
  failed: 'Failed',
  windowStart: 'Log window start',
  windowEnd: 'Log window end',
  logGap: 'Log collection schedule gap',
  fileStatus: {
    added: 'Added',
    modified: 'Modified',
    deleted: 'Deleted',
    renamed: 'Renamed',
    copied: 'Copied',
    unknown: 'Unknown status',
  },
  timeline: {
    deploymentStarted: 'Deployment attempt started',
    activationStarted: 'Container activation started',
    deploymentFinished: 'Deployment attempt ended',
    baselineFailing:
      'Predeployment baseline already recorded a runtime failure',
    logsRequested: 'Bounded log collection requested',
    logsCollected: 'Windowed log collection result saved',
    logsUnavailable: 'Windowed logs unavailable',
    finished: 'Observation task ended',
    investigated: 'Change-related investigation updated',
    evidenceExpired: 'Original evidence expired',
  },
  runtimeImageTruncated:
    'Image metadata was truncated. Collected container runtime facts remain available; complete image identity is unconfirmed.',
  targetBindingChanged:
    'Target configuration changed after these runtime facts were collected. They cannot confirm that the current target binding remains valid.',
  targetBindingUnknown:
    'The target-change flag is invalid. The current target binding is unconfirmed.',
  logsPartialScope:
    'Only the retained excerpt is available. The requested window may contain omitted or truncated content.',
  logLines: 'Collected log lines',
  reasons: {
    sourceCacheBusy:
      'The local source cache is busy; no source comparison was obtained.',
    sourceCacheUnreadable:
      'The local source cache cannot be read; source comparison is unconfirmed.',
    baseCommitNotCached:
      'The baseline commit is not cached locally; exact source comparison is unavailable.',
    currentCommitNotCached:
      'The current commit is not cached locally; exact source comparison is unavailable.',
    logTimeGap:
      'Log collection has a time gap. Missing intervals do not establish normal operation.',
    serviceRestarted:
      'Observation was interrupted by a service restart. This does not establish application failure.',
    resolvedSourceUnavailable:
      'A verified full source commit is unavailable; exact source comparison is not possible.',
    sourceRunUnavailable:
      'The corresponding source run is unavailable or unverified; its source commit cannot be confirmed.',
    sourceRevisionConflict:
      'Source commit records conflict; they cannot support an exact source comparison.',
    sourceRevisionNotConfirmed:
      'Run and deployment records have not consistently confirmed the source commit; source comparison is unavailable.',
    currentDeploymentIdentityUnavailable:
      'The current deployment identity record is unavailable; a trusted deployment comparison baseline cannot be verified.',
    deploymentHistoryUnavailable:
      'Deployment history records are unavailable; the change-comparison baseline cannot be verified.',
  },
  citationLabel: '{candidate} · {context} · Evidence {n}',
}
