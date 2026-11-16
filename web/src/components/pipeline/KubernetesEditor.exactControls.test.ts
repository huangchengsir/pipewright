import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { kubeAPI, type KubeConnection, type KubeGrant } from '../../api/kubernetes'
import type { PipelineJob, PipelineStage } from '../../api/pipeline'
import KubernetesJobConfig from './KubernetesJobConfig.vue'
import KubernetesLegacyJobConfig from './KubernetesLegacyJobConfig.vue'

const mocks=vi.hoisted(()=>({connections:vi.fn(),grants:vi.fn(),fetch:vi.fn()}))
vi.mock('../../api/kubernetes',async original=>{
  const actual=await original<typeof import('../../api/kubernetes')>()
  return {...actual,kubeAPI:{...actual.kubeAPI,connections:mocks.connections,grants:mocks.grants}}
})
const job:PipelineJob={
  id:'job',name:'Deployment',type:'deploy_kubernetes',summary:'',
  config:{connectionId:'connection-a',credentialId:'legacy-private-reference',namespace:'old-ns',timeoutSeconds:'420'},
}
const stage:PipelineStage={id:'stage',name:'Deploy',kind:'deploy',jobs:[job]}
const props={config:job.config,stages:[stage],stage,job,projectId:'fixture-project'}
const connection:KubeConnection={
  id:'connection-a',revision:1,credentialRevision:1,name:'Fixture cluster',credentialId:'legacy-private-reference',
  enabled:true,context:'fixture-context',server:'fixture.invalid',clusterKey:'cluster',profileFingerprint:'profile',
  profile:{protocol:'kubectl-sh-v1',image:'fixture',architecture:'amd64',kubectlPath:'/usr/bin/kubectl',uid:1000,network:'bridge'},executors:[],
}
const grant=(id:string,projectId:string,deployment:string):KubeGrant=>({
  id,projectId,environment:'fixture',namespace:'apps',deployment,connectionId:connection.id,
  enabled:true,containers:['app'],executor:{kind:'local'},requireApproval:false,revision:1,
})
let wrapper:VueWrapper|undefined
beforeEach(()=>{
  vi.resetAllMocks()
  mocks.fetch.mockImplementation(()=>{throw new Error('Unexpected fixture network access')})
  vi.stubGlobal('fetch',mocks.fetch)
  vi.mocked(kubeAPI.connections).mockResolvedValue([connection])
  vi.mocked(kubeAPI.grants).mockResolvedValue([
    grant('allowed','fixture-project','visible-workload'),
    grant('other','other-project','other-private-workload'),
  ])
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined;vi.unstubAllGlobals()})

it('uses the Kubernetes suite button to create a new local v2 draft without cloning legacy bindings',async()=>{
  wrapper=mount(KubernetesJobConfig,{props})
  await flushPromises()
  expect(kubeAPI.connections).toHaveBeenCalledOnce()
  expect(kubeAPI.grants).toHaveBeenCalledOnce()
  mocks.connections.mockClear()
  mocks.grants.mockClear()
  await wrapper.get('details.kube-surface button').trigger('click')
  expect(wrapper.emitted('update')).toHaveLength(1)
  const data=wrapper.emitted('update')![0]![0] as Record<string,string>
  expect(data).toMatchObject({version:'2',mode:'image',namespace:'default',images:'[]',timeoutSeconds:'300'})
  expect(data).not.toHaveProperty('credentialId')
  expect(data).not.toHaveProperty('connectionId')
  expect(props.config.credentialId).toBe('legacy-private-reference')
  expect(kubeAPI.connections).not.toHaveBeenCalled()
  expect(kubeAPI.grants).not.toHaveBeenCalled()
  expect(mocks.fetch).not.toHaveBeenCalled()
})

it('refreshes legacy Kubernetes choices after a read error and retains only the current project grants',async()=>{
  vi.mocked(kubeAPI.connections).mockRejectedValueOnce(new Error('fixture unavailable'))
  wrapper=mount(KubernetesLegacyJobConfig,{props})
  await flushPromises()
  const retry=wrapper.get('.kube-error button')
  const previous=vi.mocked(kubeAPI.connections).mock.calls[0]![0]!
  await retry.trigger('click')
  await flushPromises()
  expect(previous.aborted).toBe(true)
  expect(kubeAPI.connections).toHaveBeenCalledTimes(2)
  const signal=vi.mocked(kubeAPI.connections).mock.calls[1]![0]
  expect(kubeAPI.grants).toHaveBeenCalledExactlyOnceWith('connection-a',signal)
  const options=wrapper.get('#kj-target').text()
  expect(options).toContain('visible-workload')
  expect(options).not.toContain('other-private-workload')
  expect(wrapper.emitted('update')).toBeUndefined()
  expect(mocks.fetch).not.toHaveBeenCalled()
})
