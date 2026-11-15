import {beforeEach,afterEach,describe,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import Panel from './BatchDeployPanel.vue'
const m=vi.hoisted(()=>({decisions:vi.fn(),servers:vi.fn(),list:vi.fn(),create:vi.fn(),retry:vi.fn(),next:vi.fn()}))
vi.mock('../../api/access',()=>({access:{decisions:m.decisions}}))
vi.mock('../../api/servers',()=>({listServers:m.servers}))
vi.mock('../../api/runs',()=>({listDeployBatches:m.list,deployBatch:m.create,retryDeployBatch:m.retry,continueDeployBatch:m.next}))
let w:VueWrapper|undefined,allowed:Set<string>
const artifact={id:'a1',type:'jar' as const,name:'app',reference:'hash',sizeBytes:1,metadata:{},createdAt:''}
const batch={id:'b1',runId:'r1',status:'failed',createdAt:'2026-11-13',updatedAt:'',items:[{id:'t1',status:'failed',artifactName:'private-artifact',serverName:'server',serverId:'s1',deployConfig:{path:'/app'}},{id:'t2',status:'pending',artifactName:'pending',serverName:'server',serverId:'s1',deployConfig:{path:'/other'}}]}
async function settle(){for(let i=0;i<5;i++)await flushPromises()}
async function render(){w=mount(Panel,{props:{runId:'r1',projectId:'p1',environmentId:'e1',artifacts:[artifact],canCreate:true}});await settle()}
beforeEach(()=>{setActivePinia(createPinia());useSessionStore().setUser({username:'employee',userId:'u1',multiTenant:true,instanceAdmin:false});usePermissionStore().setTeam('a');allowed=new Set(['run.read','deployment.execute','pipeline.edit']);vi.clearAllMocks();m.decisions.mockImplementation(async(team:string,qs:{action:string}[])=>({userId:'u1',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(q=>allowed.has(q.action))}));m.servers.mockResolvedValue([{id:'s1',name:'server'}]);m.list.mockResolvedValue({batches:[batch]});m.create.mockResolvedValue(batch);m.retry.mockResolvedValue(batch);m.next.mockResolvedValue(batch)})
afterEach(()=>{w?.unmount();w=undefined})
it('does not read without run permission',async()=>{allowed.clear();await render();expect(m.list).not.toHaveBeenCalled();expect(m.servers).not.toHaveBeenCalled()})
it('reads history with fixed team without exposing mutation controls',async()=>{allowed=new Set(['run.read']);await render();expect(w!.text()).toContain('private-artifact');expect(m.list).toHaveBeenCalledWith('r1',expect.objectContaining({headers:{'X-Pipewright-Team':'a'},signal:expect.any(AbortSignal)}));expect(w!.find('.batch-button').exists()).toBe(false);expect(w!.find('.history-actions button').exists()).toBe(false)})
it('requires project edit permission for caller-authored paths',async()=>{allowed.delete('pipeline.edit');await render();expect(w!.find('.batch-button').exists()).toBe(false);expect(w!.findAll('.history-actions button')).toHaveLength(2);expect(m.servers).not.toHaveBeenCalled()})
it('creates a batch only with both permissions and fixed identity',async()=>{await render();await w!.get('.batch-button').trigger('click');await w!.get('.server-list input').setValue(true);await w!.get('.row-fields input').setValue('/app');await w!.get('.submit-button').trigger('click');await settle();expect(m.create).toHaveBeenCalledWith('r1',expect.objectContaining({items:[{artifactId:'a1',serverIds:['s1'],deployConfig:{path:'/app'}}]}),expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}));expect(w!.emitted('completed')).toHaveLength(1)})
it.each(['retry','continue'])('executes authorized %s using fixed team',async(mode)=>{await render();await w!.findAll('.history-actions button')[mode==='retry'?0:1]!.trigger('click');await settle();expect(mode==='retry'?m.retry:m.next).toHaveBeenCalledWith('r1','b1',expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}))})
it.each(['team','employee','permission'])('discards late history after %s change',async(kind)=>{let finish!:(x:unknown)=>void;m.list.mockImplementation(()=>new Promise(r=>{finish=r}));await render();if(kind==='team')usePermissionStore().setTeam('b');else if(kind==='employee')useSessionStore().setUser({username:'other',userId:'u2',multiTenant:true});else{allowed.clear();usePermissionStore().invalidate()}await settle();finish({batches:[batch]});await settle();expect(w!.text()).not.toContain('private-artifact');expect(w!.find('.history-actions button').exists()).toBe(false)})
it('clears prepared rows and blocks captured submit after write revocation',async()=>{await render();await w!.get('.batch-button').trigger('click');await w!.get('.server-list input').setValue(true);await w!.get('.row-fields input').setValue('/app');const button=w!.get('.submit-button');allowed.delete('deployment.execute');usePermissionStore().invalidate();await settle();await button.trigger('click');expect(m.create).not.toHaveBeenCalled();expect(w!.find('.batch-form').exists()).toBe(false)})
it('does not emit completion from an old employee retry',async()=>{let finish!:(x:unknown)=>void;m.retry.mockImplementation(()=>new Promise(r=>{finish=r}));await render();await w!.get('.history-actions button').trigger('click');useSessionStore().setUser({username:'other',userId:'u2',multiTenant:true});await settle();finish(batch);await settle();expect(w!.emitted('completed')).toBeUndefined()})

describe('batch row exact DOM actions', () => {
  async function addDraft(path: string) {
    await w!.get('.batch-button').trigger('click')
    const row = w!.findAll('.batch-row').at(-1)!
    await row.get('.server-list input').setValue(true)
    await row.get('.row-fields input').setValue(path)
    return row
  }

  it('removes only the selected row and never another row on a retained click', async () => {
    await render()
    await addDraft('/first')
    const removed = await addDraft('/selected')
    await addDraft('/third')
    const retained = removed.get('.remove-button')
    await retained.trigger('click')
    await settle()
    expect(w!.findAll('.batch-row')).toHaveLength(2)
    await retained.trigger('click')
    await settle()
    expect(w!.findAll('.batch-row')).toHaveLength(2)
    expect(m.create).not.toHaveBeenCalled()
    await w!.get('.submit-button').trigger('click')
    await settle()
    expect(m.create).toHaveBeenCalledExactlyOnceWith('r1',expect.objectContaining({
      items: ['/first','/third'].map(path => ({ artifactId: 'a1', serverIds: ['s1'], deployConfig: {path} })),
    }),expect.objectContaining({headers:{'X-Pipewright-Team':'a'},signal:expect.any(AbortSignal)}))
  })

  it('does not reuse a removed draft control after pipeline edit is revoked and restored', async () => {
    await render()
    const original = await addDraft('/cleared')
    const retained = original.get('.remove-button')
    allowed.delete('pipeline.edit')
    usePermissionStore().invalidate()
    await settle()
    expect(w!.find('.batch-form').exists()).toBe(false)
    await retained.trigger('click')
    allowed.add('pipeline.edit')
    usePermissionStore().invalidate()
    await settle()
    await addDraft('/new-draft')
    await retained.trigger('click')
    await settle()
    expect(w!.findAll('.batch-row')).toHaveLength(1)
    expect(w!.get('.row-fields input').element).toHaveProperty('value','/new-draft')
    expect(m.create).not.toHaveBeenCalled()
  })
})
