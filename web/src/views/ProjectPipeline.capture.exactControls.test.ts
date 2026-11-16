import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useSessionStore } from '../stores/session'
import { http } from '../api/http'
import { bindConfigRevision } from '../api/configRevision'
import type { PipelineDTO, PipelineStage } from '../api/pipeline'
import ProjectPipeline from './ProjectPipeline.vue'

vi.mock('../api/http',async original=>({
  ...await original<typeof import('../api/http')>(),
  http:{get:vi.fn(),put:vi.fn(),post:vi.fn(),patch:vi.fn()},
}))
let wrapper:VueWrapper|undefined
let pinia:ReturnType<typeof createPinia>
let pipelineReads:number
let finish:(v:PipelineDTO)=>void
const Canvas=defineComponent({
  props:{stages:{type:Array,required:true}},
  setup(props){
    return ()=>h('div',[
      h('span',{class:'fixture-latest-stage'},(props.stages[0] as PipelineStage).name),
      h('input',{class:'fixture-bubbles',onClick:(e:Event)=>e.stopPropagation(),onInput:(e:Event)=>e.stopPropagation(),onChange:(e:Event)=>e.stopPropagation()}),
    ])
  },
})
const pipeline=(name:string,version:string)=>bindConfigRevision<PipelineDTO>({
  stages:[{id:'stage',name,kind:'build',jobs:[]}],yaml:'',status:'draft',updatedAt:'',
},version)
beforeEach(()=>{
  vi.resetAllMocks()
  pipelineReads=0
  pinia=createPinia();setActivePinia(pinia)
  useSessionStore().setUser({username:'owner',multiTenant:false})
  vi.mocked(http.get).mockImplementation(async (url:string)=>{
    if(url==='/api/projects')return [{id:'p',name:'fixture',configRevision:'cfg-2'}]
    if(url==='/api/projects/p/pipeline'){
      if(++pipelineReads===1)return pipeline('Initial stage','cfg-1')
      if(pipelineReads===2)return new Promise<PipelineDTO>(resolve=>{finish=resolve})
      return pipeline('Unexpected further read','cfg-2')
    }
    if(url==='/api/projects/p/pipeline/settings')return bindConfigRevision({
      build:{model:'dockerfile',dockerfilePath:'Dockerfile',toolchain:{language:'',version:''},artifactType:'image',
        vars:[],cache:{enabled:false,paths:[]}},
      environments:[],updatedAt:'',
    },'cfg-2')
    if(url==='/api/credentials')return []
    if(['/api/servers','/api/notifications/channel-options','/api/dns/providers'].includes(url))return {items:[]}
    throw Error('Unexpected fixture endpoint '+url)
  })
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined})

async function open(){
  const router=createRouter({history:createMemoryHistory(),routes:[
    {path:'/projects/:id/pipeline',component:ProjectPipeline},
    {path:'/projects',component:{template:'<main>Projects</main>'}},
  ]})
  await router.push('/projects/p/pipeline')
  await router.isReady()
  wrapper=mount(ProjectPipeline,{global:{plugins:[pinia,router],stubs:{
    PipelineCanvas:Canvas, VarsCacheTab:true, EnvCredsTab:true, TriggersPanel:true, ProjectPreviewConfig:true,
    PipelineGuide:true, RiskAnnotationPanel:true, ValidationPanel:true, CronPanel:true, ParametersPanel:true,
    ConcurrencyPanel:true, ChainPanel:true, RunnerPanel:true, EnvironmentsPanel:true,
  }}})
  await flushPromises()
  expect(pipelineReads).toBe(2)
  expect(wrapper.get('.fixture-latest-stage').text()).toBe('Initial stage')
  return wrapper
}
it.each(['input','change','click'])('captures a child %s before bubbling and rejects the late pristine reread',async event=>{
  const view=await open()
  const target=view.get('.pipeline-root .fixture-bubbles')
  await target.trigger(event)
  finish(pipeline('Reread stage','cfg-2'))
  await flushPromises()
  expect(view.get('.fixture-latest-stage').text()).toBe('Initial stage')
  expect(pipelineReads).toBe(2)
  expect(http.put).not.toHaveBeenCalled()
  expect(http.patch).not.toHaveBeenCalled()
})
it('accepts the same reread when no initial interaction was captured',async()=>{
  const view=await open()
  finish(pipeline('Reread stage','cfg-2'))
  await flushPromises()
  expect(view.get('.fixture-latest-stage').text()).toBe('Reread stage')
  expect(http.put).not.toHaveBeenCalled()
})
