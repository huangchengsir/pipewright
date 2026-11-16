import {mount,flushPromises,DOMWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {createI18n} from 'vue-i18n'
import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import Settings from './SettingsServers.vue'
import ContainerAiPanel from '../../components/ops/ContainerAiPanel.vue'
import ServiceLogViewer from '../../components/ops/ServiceLogViewer.vue'
import ServiceOpsPanel from '../../components/ops/ServiceOpsPanel.vue'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import {access} from '../../api/access'
import * as api from '../../api/servers'
import {listCredentials} from '../../api/credentials'
vi.mock('vue-router',()=>({useRouter:()=>({resolve:()=>({href:'/terminal'})})}))
vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/servers',()=>({listServers:vi.fn(),createServer:vi.fn(),updateServer:vi.fn(),deleteServer:vi.fn(),testServer:vi.fn()}))
vi.mock('../../api/credentials',()=>({listCredentials:vi.fn()}))
let allowed:Set<string>,wrappers:ReturnType<typeof mount>[]=[]
const server={id:'s',name:'Scoped host',host:'example.invalid',port:22,user:'builder',credentialId:'c',credentialName:'SSH',createdAt:'',updatedAt:''}
beforeEach(()=>{setActivePinia(createPinia());vi.clearAllMocks();allowed=new Set(['server.inspect:s']);useSessionStore().setUser({username:'member',userId:'u',multiTenant:true});usePermissionStore().setTeam('a');vi.mocked(access.decisions).mockImplementation(async(team,queries)=>{return {teamId:team,userId:'u',catalogVersion:1,modeEpoch:1,revision:1,allowed:queries.map(q=>team==='a'&&allowed.has(q.action+':'+q.resourceId))}});vi.mocked(api.listServers).mockResolvedValue([server]);vi.mocked(listCredentials).mockResolvedValue([{id:'c',name:'SSH',type:'ssh_password',scope:'',username:'builder',lastUsedAt:null,maskedValue:'***',createdAt:''}]);vi.mocked(api.updateServer).mockResolvedValue(server);vi.mocked(api.createServer).mockResolvedValue({...server,id:'new'});vi.mocked(api.deleteServer).mockResolvedValue(undefined)})
afterEach(()=>{wrappers.splice(0).forEach(w=>w.unmount());vi.restoreAllMocks()})
function view(){const w=mount(Settings,{global:{plugins:[createI18n({legacy:false,locale:'en',messages:{en:{}},missingWarn:false,fallbackWarn:false})],stubs:{Teleport:false,ContainerAiPanel:true,ServerExecutionAssignment:true,ServiceLogViewer:true,ServiceOpsPanel:true}}});wrappers.push(w);return w}
function button(w:ReturnType<typeof mount>,label:string){return w.findAll('button').find(b=>b.text()===label)}
it('shows scoped server even when credential metadata is not readable',async()=>{vi.mocked(listCredentials).mockRejectedValue(new Error('forbidden'));const w=view();await flushPromises();expect(w.text()).toContain('Scoped host');expect(button(w,'settingsServers.edit')).toBeUndefined();expect(button(w,'settingsServers.delete')).toBeUndefined();expect(button(w,'settingsServers.testConnection')).toBeUndefined();expect(api.testServer).not.toHaveBeenCalled()})
it('requires edit and the selected credential before form submission',async()=>{allowed.add('server.edit:s');const w=view();await flushPromises();await button(w,'settingsServers.edit')!.trigger('click');await flushPromises();expect(new DOMWrapper(document.body).find('button[type=submit]').exists()).toBe(false);await new DOMWrapper(document.body).get('form').trigger('submit');expect(api.updateServer).not.toHaveBeenCalled();allowed.add('credential.use:c');usePermissionStore().invalidate();await flushPromises();await new DOMWrapper(document.body).get('form').trigger('submit');await flushPromises();expect(api.updateServer).toHaveBeenCalledWith('s',expect.objectContaining({host:'example.invalid',credentialId:'c'}),serverScope())})
it('allows creation only after selecting a usable credential',async()=>{allowed.add('server.create:');allowed.add('credential.use:c');const w=view();await flushPromises();await button(w,'settingsServers.addServer')!.trigger('click');await flushPromises();const fields=new DOMWrapper(document.body).findAll('input');await fields[0]!.setValue('New server');await fields[1]!.setValue('new.invalid');await fields[3]!.setValue('builder');await new DOMWrapper(document.body).get('form').trigger('submit');await flushPromises();expect(api.createServer).toHaveBeenCalledWith(expect.objectContaining({name:'New server',host:'new.invalid',credentialId:'c'}),serverScope())})
it('rechecks deletion after the confirmation was opened',async()=>{allowed.add('server.delete:s');const w=view();await flushPromises();await button(w,'settingsServers.delete')!.trigger('click');await flushPromises();const submit=new DOMWrapper(document.body).findAll('button').find(b=>b.text()==='settingsServers.confirmDelete')!;allowed.delete('server.delete:s');usePermissionStore().invalidate();await flushPromises();await submit.trigger('click');expect(api.deleteServer).not.toHaveBeenCalled();expect(new DOMWrapper(document.body).find('[aria-labelledby=server-del-title]').exists()).toBe(false)})
it('requires terminal and credential actions independently from inspect',async()=>{const open=vi.spyOn(window,'open').mockReturnValue(null);const w=view();await flushPromises();allowed.add('server.terminal:s');usePermissionStore().invalidate();await flushPromises();expect(button(w,'settingsServers.terminal')).toBeUndefined();allowed.add('credential.use:c');usePermissionStore().invalidate();await flushPromises();await button(w,'settingsServers.terminal')!.trigger('click');expect(open).toHaveBeenCalledWith('/terminal','_blank','noopener')})
it('discards old team test results and clears logout data',async()=>{allowed.add('server.use:s');allowed.add('credential.use:c');let finish!:(r:api.ServerTestResult)=>void;vi.mocked(api.testServer).mockReturnValue(new Promise(resolve=>{finish=resolve}));const w=view();await flushPromises();await button(w,'settingsServers.testConnection')!.trigger('click');usePermissionStore().setTeam('b');await flushPromises();finish({ok:true,latencyMs:1,output:'old private output',error:null});await flushPromises();expect(w.text()).not.toContain('old private output');expect(w.text()).not.toContain('Scoped host');useSessionStore().clearSession();await flushPromises();expect(w.find('.server-row').exists()).toBe(false)})
it('clears old rows on network failure instead of retaining privileged data',async()=>{const w=view();await flushPromises();expect(w.text()).toContain('Scoped host');useSessionStore().isNetworkError=true;await flushPromises();expect(w.text()).not.toContain('Scoped host')})

it('shows monitoring approval separately from connectivity and performs no sampling',async()=>{
 vi.mocked(api.listServers).mockResolvedValue([{...server,metricsAuthorizationState:'required'}]);
 const w=view();await flushPromises();expect(w.get('.monitoring-authorization').text()).toBe('settingsServers.monitoringRequired');
 expect(w.get('.monitoring-authorization').attributes('title')).toBe('settingsServers.monitoringHint');expect(api.testServer).not.toHaveBeenCalled();
 useSessionStore().clearSession();await flushPromises();expect(w.find('.monitoring-authorization').exists()).toBe(false);
})

const serverScope = () => expect.objectContaining({
  headers: {'X-Pipewright-Team':'a'},
  signal: expect.any(AbortSignal),
})
const modal = (label: string) => new DOMWrapper(document.body).get('[aria-labelledby=' + label + ']')

it('pins the initial server and credential list to the selected team and aborts their old signal', async () => {
  const w = view()
  await flushPromises()
  expect(w.text()).toContain('Scoped host')
  expect(api.listServers).toHaveBeenCalledExactlyOnceWith(serverScope())
  expect(listCredentials).toHaveBeenCalledExactlyOnceWith(serverScope())
  const options = vi.mocked(api.listServers).mock.calls[0]![0]!
  usePermissionStore().setTeam('b')
  await flushPromises()
  expect(options.signal?.aborted).toBe(true)
  expect(api.listServers).toHaveBeenLastCalledWith(expect.objectContaining({headers:{'X-Pipewright-Team':'b'}}))
  expect(w.text()).not.toContain('Scoped host')
})

it.each(['create','edit'] as const)('clicks the actual server %s submit with a fixed team and permitted credential', async action => {
  allowed.add('server.create:')
  allowed.add('server.edit:s')
  allowed.add('credential.use:c')
  const w = view()
  await flushPromises()
  await button(w,action === 'create' ? 'settingsServers.addServer' : 'settingsServers.edit')!.trigger('click')
  await flushPromises()
  const form = modal('server-modal-title')
  const fields = form.findAll('input')
  await fields[0]!.setValue('Edited host')
  await fields[1]!.setValue('edited.invalid')
  await fields[3]!.setValue('builder')
  ;(form.get('button[type=submit]').element as HTMLButtonElement).click()
  await flushPromises()
  const payload = expect.objectContaining({name:'Edited host',host:'edited.invalid',credentialId:'c'})
  if (action === 'create') {
    expect(api.createServer).toHaveBeenCalledExactlyOnceWith(payload, serverScope())
    expect(api.updateServer).not.toHaveBeenCalled()
  } else {
    expect(api.updateServer).toHaveBeenCalledExactlyOnceWith('s', payload, serverScope())
    expect(api.createServer).not.toHaveBeenCalled()
  }
})

it('clicks both test and delete confirmation for exactly the authorized row and team', async () => {
  allowed.add('server.inspect:second')
  allowed.add('server.use:second')
  allowed.add('server.delete:second')
  allowed.add('credential.use:c')
  vi.mocked(api.listServers).mockResolvedValue([server, {...server,id:'second',name:'Second host'}])
  vi.mocked(api.testServer).mockResolvedValue({ok:true,latencyMs:1,output:'private test output',error:null})
  const w = view()
  await flushPromises()
  const rows = w.findAll('.server-row')
  expect(rows[0]!.findAll('button').map(row => row.text())).not.toContain('settingsServers.testConnection')
  await rows[1]!.findAll('button').find(el => el.text()==='settingsServers.testConnection')!.trigger('click')
  await flushPromises()
  expect(api.testServer).toHaveBeenCalledExactlyOnceWith('second', serverScope())
  expect(w.text()).toContain('private test output')
  allowed.delete('server.use:second')
  usePermissionStore().invalidate()
  await flushPromises()
  expect(w.text()).not.toContain('private test output')
  await rows[1]!.findAll('button').find(el => el.text()==='settingsServers.delete')!.trigger('click')
  expect(api.deleteServer).not.toHaveBeenCalled()
  await modal('server-del-title').get('button.btn-danger').trigger('click')
  await flushPromises()
  expect(api.deleteServer).toHaveBeenCalledExactlyOnceWith('second', serverScope())
  expect(w.text()).not.toContain('Second host')
  expect(w.text()).toContain('Scoped host')
})

it('displays only the actual connection test and AI controls allowed for the exact server', async () => {
  allowed.add('server.inspect:second')
  allowed.add('server.use:second')
  allowed.add('credential.use:c')
  vi.mocked(api.listServers).mockResolvedValue([server, {...server,id:'second',name:'Second host'}])
  const w = view()
  await flushPromises()
  const rows = w.findAll('.server-row')
  expect(rows[0]!.findAll('button').map(control => control.text())).not.toContain('opsChat.title')
  expect(rows[1]!.findAll('button').map(control => control.text())).not.toContain('opsChat.title')
  allowed.add('opschat.create:new-resource')
  usePermissionStore().invalidate()
  await flushPromises()
  await w.findAll('.server-row')[1]!.findAll('button').find(control => control.text()==='opsChat.title')!.trigger('click')
  expect(w.getComponent(ContainerAiPanel).props('initialServerIds')).toEqual(['second'])
  allowed.delete('opschat.create:new-resource')
  usePermissionStore().invalidate()
  await flushPromises()
  expect(w.find('container-ai-panel-stub').exists()).toBe(false)
  allowed.add('opschat.create:new-resource')
  usePermissionStore().invalidate()
  await flushPromises()
  expect(w.find('container-ai-panel-stub').exists()).toBe(false)
})

it.each([
  ['settingsServers.logs','server-logs-title','close'],
  ['settingsServers.logs','server-logs-title','backdrop'],
  ['settingsServers.serviceOps','server-ops-title','close'],
  ['settingsServers.serviceOps','server-ops-title','backdrop'],
] as const)('opens the exact scoped %s and dismisses it using %s %s', async (label, title, dismissal) => {
  allowed.add('server.use:s')
  allowed.add('credential.use:c')
  const w = view()
  await flushPromises()
  await button(w,label)!.trigger('click')
  expect(modal(title).text()).toContain('builder@example.invalid:22')
  const serverId = title === 'server-logs-title'
    ? w.getComponent(ServiceLogViewer).props('serverId')
    : w.getComponent(ServiceOpsPanel).props('serverId')
  expect(serverId).toBe('s')
  if (dismissal === 'close') await modal(title).get('button.btn-ghost').trigger('click')
  else await new DOMWrapper(modal(title).element.parentElement!).trigger('click')
  expect(document.querySelector('[aria-labelledby=' + title + ']')).toBeNull()
  expect(api.testServer).not.toHaveBeenCalled()
})

it.each([
  ['settingsServers.addServer','server-modal-title','cancel'],
  ['settingsServers.edit','server-modal-title','backdrop'],
  ['settingsServers.delete','server-del-title','cancel'],
  ['settingsServers.delete','server-del-title','backdrop'],
] as const)('dismisses the rendered %s dialog with %s %s without writes', async (label, title, dismissal) => {
  allowed.add('server.create:')
  allowed.add('server.edit:s')
  allowed.add('server.delete:s')
  allowed.add('credential.use:c')
  const w = view()
  await flushPromises()
  await button(w,label)!.trigger('click')
  await flushPromises()
  if (dismissal === 'cancel') await modal(title).get('.modal-actions button.btn-ghost').trigger('click')
  else await new DOMWrapper(modal(title).element.parentElement!).trigger('click')
  expect(document.querySelector('[aria-labelledby=' + title + ']')).toBeNull()
  expect(api.createServer).not.toHaveBeenCalled()
  expect(api.updateServer).not.toHaveBeenCalled()
  expect(api.deleteServer).not.toHaveBeenCalled()
})

it('retries the list using the current selected team and performs no write', async () => {
  vi.mocked(api.listServers).mockRejectedValueOnce(new Error('synthetic read failure'))
  const w = view()
  await flushPromises()
  await w.get('button.banner-retry').trigger('click')
  await flushPromises()
  expect(api.listServers).toHaveBeenCalledTimes(2)
  expect(api.listServers).toHaveBeenLastCalledWith(serverScope())
  expect(w.text()).toContain('Scoped host')
  expect(api.testServer).not.toHaveBeenCalled()
})
