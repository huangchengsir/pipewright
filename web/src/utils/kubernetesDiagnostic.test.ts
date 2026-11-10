import {describe,it,expect,afterEach} from 'vitest';
import {i18n,setLocale,SUPPORTED_LOCALES} from '../i18n';
import {HttpError} from '../api/http';
import {kubeError} from '../api/kubernetes';
import {kubePreparationDiagnostic,kubernetesDiagnosticKeys} from './kubernetesDiagnostic';
afterEach(()=>setLocale('zh-CN'));
describe('closed Kubernetes preparation diagnostics',()=>{
 for(const {code} of SUPPORTED_LOCALES) it(code+' renders all API failures without internal codes or remote text',()=>{
  setLocale(code);
  for(const reason of Object.keys(kubernetesDiagnosticKeys)){
   const result=kubePreparationDiagnostic('kubernetes preparation: phase=dry_run reason='+reason,i18n.global.t)!;
   expect(result).not.toContain(reason);expect(result).not.toContain('kubernetes.');expect(result).not.toContain('reason=');
   expect(result).toContain(i18n.global.t(kubernetesDiagnosticKeys[reason]));
  }
  const unknown=kubePreparationDiagnostic('kubernetes preparation: phase=PrivateCanary reason=PrivateCanary',i18n.global.t)!;
  expect(unknown).not.toContain('PrivateCanary');expect(unknown).toContain(i18n.global.t('kubernetes.errors.preparationFailed'));
  expect(kubeError(new HttpError(409,{code:'kubernetes_resource_conflict',message:'PrivateCanary'},'PrivateCanary'))).toBe('kubernetes.errors.resourceConflict');
 });
 it('leaves external command output untouched and translates only the owned grammar',()=>{
  expect(kubePreparationDiagnostic('external tool says kubernetes_resource_conflict',i18n.global.t)).toBeUndefined();
 });
});

describe('phase feedback translations',()=>{
 for(const {code} of SUPPORTED_LOCALES) it(code+' includes all v2 phase messages and honest empty state',()=>{
  setLocale(code);
  for(const key of ['resource_identity_check','resource_write_fenced','resource_health_check','release_ready','batch_ready_waiting_action']){
   const path='kubernetes.phaseCode.'+key;expect(i18n.global.te(path)).toBe(true);expect(i18n.global.t(path)).not.toBe(path);
  }
  expect(i18n.global.te('kubernetes.release.noPhaseEvents')).toBe(true);
  expect(i18n.global.t('kubernetes.release.healthObservation',{kind:'Pod',state:'pending'})).toContain('Pod');
 });
});


describe('closed Kubernetes runtime observation diagnostics',()=>{
 for(const {code} of SUPPORTED_LOCALES) it(code+' renders first observation failure with existing friendly messages',()=>{
  setLocale(code);
  for(const [stage,reason] of [['resource_read','kubernetes_tool_unavailable'],['related_list_read','kubernetes_private_output_limit'],['related_list_decode','kubernetes_invalid_input'],['related_anchor_read','kubernetes_not_found'],['related_filter','kubernetes_not_authorized'],['health_evaluation','kubernetes_workload_not_ready'],['observation_wait','observation_timeout']]){
   const result=kubePreparationDiagnostic('kubernetes observe: stage='+stage+' reason='+reason,i18n.global.t);
   expect(result).toBeTruthy();expect(result).not.toContain('kubernetes.');expect(result).not.toContain('reason=');expect(result).not.toContain(stage);expect(result).not.toContain(reason);
  }
  const unknown=kubePreparationDiagnostic('kubernetes observe: stage=PrivateCanary reason=PrivateCanary',i18n.global.t)!;
  expect(unknown).toBeTruthy();expect(unknown).not.toContain('PrivateCanary');expect(unknown).toContain(i18n.global.t('kubernetes.errors.observation'));
 });
});
