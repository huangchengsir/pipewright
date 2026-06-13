export default {
  // ─── Container states ───
  stateRunning: 'Running',
  statePaused: 'Paused',
  stateRestarting: 'Restarting',
  stateCreated: 'Created',
  stateExited: 'Exited',
  stateDead: 'Dead',
  stateUnknown: 'Unknown',

  // ─── Container lifecycle action buttons ───
  actionStart: 'Start',
  actionRestart: 'Restart',
  actionStop: 'Stop',
  actionPause: 'Pause',
  actionUnpause: 'Resume',
  actionKill: 'Kill',
  actionRm: 'Remove',

  // ─── Action button hover hints ───
  hintStart: 'Start a stopped container (docker start), launching a fresh process from scratch.',
  hintRestart: 'Restart: gracefully stop first (SIGTERM, 10s grace), then start (docker restart).',
  hintStop: 'Graceful stop: send SIGTERM, then SIGKILL if it does not exit within 10s (docker stop). Use this for everyday shutdowns so the program can clean up and flush to disk.',
  hintPause: 'Pause: cgroup-freeze all processes in the container (docker pause); memory is kept as-is and the CPU no longer schedules it. Click "Resume" to continue from where it left off. Does not free memory.',
  hintUnpause: 'Resume: unfreeze a paused container (docker unpause); the same process continues from where it stopped.',
  hintKill: 'Force Kill: send SIGKILL to terminate immediately with no chance to clean up (docker kill); unflushed data may be lost. Use only when "Stop" is stuck.',
  hintRm: 'Remove the container (docker rm); a running one must be stopped first. The container config is removed, but mounted data volumes are unaffected.',

  // ─── Destructive action confirmation ───
  dangerRestartTitle: 'Restart container {n}?',
  dangerRestartBody: 'The container will be stopped and started again; its service will be briefly unavailable.',
  dangerRestartConfirm: 'Confirm restart',
  dangerStopTitle: 'Stop container {n}?',
  dangerStopBody: 'The container will be stopped, interrupting the service it provides until it is started again.',
  dangerStopConfirm: 'Confirm stop',
  dangerKillTitle: 'Force Kill container {n}?',
  dangerKillBody: 'SIGKILL will be sent to terminate the container process immediately; unflushed data may be lost.',
  dangerKillConfirm: 'Force Kill',
  dangerRmTitle: 'Remove container {n}?',
  dangerRmBody: 'The container will be removed (a running one must be stopped first). Its config is removed with it; data volumes are unaffected.',
  dangerRmConfirm: 'Confirm remove',

  // ─── Parameter types ───
  paramTypeString: 'Text',
  paramTypeChoice: 'Enum',
  paramTypeBoolean: 'Boolean',
  paramTypeNumber: 'Number',

  // ─── Parameter value validation ───
  paramRequired: 'Parameter "{label}" is required',
  paramNotNumber: 'Parameter "{label}" must be a number',
  paramNotBoolean: 'Parameter "{label}" must be true/false',
  paramNotInChoice: 'Parameter "{label}" is not among the available options',

  // ─── Promotion status ───
  promotionPromoted: 'Promoted',
  promotionPending: 'Pending approval',
  promotionRejected: 'Rejected',

  // ─── Environment name validation ───
  envNameEmpty: 'Environment name cannot be empty',
  envNameInvalid: 'Environment name may only contain letters, digits, hyphens, and underscores',
  envNameTooLong: 'Environment name cannot exceed 64 characters',

  // ─── Concurrency limit ───
  concurrencyNotInteger: 'The concurrency limit must be an integer',
  concurrencyTooSmall: 'The concurrency limit cannot be less than {min}',
  concurrencyTooLarge: 'The concurrency limit cannot exceed {max}',
  concurrencyUnlimited: 'Unlimited',

  // ─── Terminal ───
  terminalSessionEnded: 'Terminal session ended',
}
