type Translate = (key: string, params?: Record<string, string>) => string;
export const kubernetesDiagnosticKeys: Record<string, string> = {
  kubernetes_request_invalid: 'kubernetes.errors.requestInvalid',
  kubernetes_credential_rejected: 'kubernetes.errors.credentialRejected',
  kubernetes_not_authorized: 'kubernetes.errors.denied',
  kubernetes_not_found: 'kubernetes.errors.missing',
  kubernetes_resource_conflict: 'kubernetes.errors.resourceConflict',
  kubernetes_resource_invalid: 'kubernetes.errors.resourceInvalid',
  kubernetes_api_throttled: 'kubernetes.errors.apiThrottled',
  kubernetes_api_unavailable: 'kubernetes.errors.apiUnavailable',
  kubernetes_admission_mismatch: 'kubernetes.errors.admissionMismatch',
  dry_run_failed: 'kubernetes.errors.validationFailed',
  preparation_failed: 'kubernetes.errors.preparationFailed',
};
const existingReasons: Record<string, string> = {
  kubernetes_invalid_input: 'invalid', kubernetes_revision_conflict: 'conflict',
  kubernetes_in_use: 'inUse', kubernetes_workload_held: 'held',
  kubernetes_credential_unavailable: 'credentialUnavailable', secret_runtime_unverified: 'runtime',
  kubernetes_runtime_identity_changed: 'runtime', kubernetes_tool_unavailable: 'runtime',
  kubernetes_tool_version_incompatible: 'runtime', kubernetes_transport_outcome_unknown: 'observation',
};
const phaseGroups: Record<string, string> = Object.fromEntries([
  ...['configuration','snapshot','variables','source','images','chart','values','manifest','helm','image','progressive'].map(x => [x,'configuration']),
  ...['runtime','transport','engine','pull','inspect','runtime_register','container_create','container_start','capability','helper_init','version'].map(x => [x,'runtime']),
  ['authorization','authorization'], ...['discovery','baseline','dry_run'].map(x => [x,'validation']),
  ...['plan','release_record'].map(x => [x,'approval']), ['prepare','prepare'],
]);
// Translate only this closed Pipewright diagnostic grammar. External command
// output remains verbatim; unknown diagnostic fields never echo raw values.
export function kubePreparationDiagnostic(text: string, t: Translate): string | undefined {
  if (text.startsWith('kubernetes observe: stage=')) {
    const match = /^kubernetes observe: stage=([a-z_]+) reason=([a-z_]+)$/.exec(text);
    const stages: Record<string,string> = {resource_read:'validation',related_list_read:'validation',related_list_decode:'validation',related_anchor_read:'validation',related_filter:'validation',health_evaluation:'validation',observation_wait:'runtime'};
    const phase = match ? stages[match[1]] ?? 'validation' : 'validation';
    const reason = match ? kubernetesDiagnosticKeys[match[2]] ?? (existingReasons[match[2]] ? 'kubernetes.errors.' + existingReasons[match[2]] : 'kubernetes.errors.observation') : 'kubernetes.errors.observation';
    return t('kubernetes.release.preparationMessage', {phase:t('kubernetes.release.preparationPhases.'+phase),reason:t(reason)});
  }
  if (!text.startsWith('kubernetes preparation:')) return undefined;
  const match = /^kubernetes preparation: phase=([a-z_]+) reason=([a-z_]+)$/.exec(text);
  const phase = match ? phaseGroups[match[1]] ?? 'prepare' : 'prepare';
  const reason = match ? kubernetesDiagnosticKeys[match[2]] ?? (existingReasons[match[2]] ? 'kubernetes.errors.' + existingReasons[match[2]] : 'kubernetes.errors.preparationFailed') : 'kubernetes.errors.preparationFailed';
  return t('kubernetes.release.preparationMessage', {phase: t('kubernetes.release.preparationPhases.' + phase), reason: t(reason)});
}
