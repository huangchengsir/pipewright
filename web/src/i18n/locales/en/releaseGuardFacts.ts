export default {
  healthConditionChanged:
    "The Docker health-check condition changed; this is not revalidation under the original conditions.",
  healthComparisonUnknown:
    "The original health condition cannot be verified; identical conditions are not established.",
  identityMissing: "A verifiable container or connection identity is missing.",
  digestMissing: "The repository digest cannot be verified.",
  targetChanged:
    "The target identity or configuration changed; this binding cannot continue.",
  replaced: "A later deployment replaced this target; the old window ended.",
  notFound: "The bound container no longer exists.",
  exited: "The bound container exited or was killed by OOM.",
  restarted: "A restart was observed.",
  versionMatched: "The expected repository digest matches.",
  oldVersion: "A different version was confirmed after the readiness deadline.",
  restartStable: "No restart increase was observed.",
  running: "The container was running at this sample.",
  healthGrace: "The health startup grace period is still active.",
  healthy: "Docker reports healthy.",
  unhealthy: "Docker reports unhealthy.",
  healthUnknown: "Docker health could not be established.",
  healthAbsent:
    "No Docker health check is configured; business health is not covered.",
  collectionUnavailable:
    "No valid sample could be collected. This alone does not prove an application failure.",
  truncated: "The collected result was truncated.",
  deadline: "The observation deadline was reached.",
  userStopped: "The user requested a stop.",
  interrupted: "The service interrupted this observation.",
  persistenceError: "Evidence could not be saved.",
  runtimeSample: "Typed runtime sample",
  baselineSample: "Immediate predeployment sample",
  durationChanged: "Observation duration changed.",
  logChanged: "Log scope changed.",
  targetsChanged: "Target scope changed.",
  environmentChanged: "Environment changed.",
  rolledBack:
    "The deployment rolled back; the intended new version is not established.",
  outcomeUnknown: "The deployment outcome is unknown.",
  deploymentFailed: "The deployment failed.",
  pendingActivation: "The container identity will be bound after activation.",
};
