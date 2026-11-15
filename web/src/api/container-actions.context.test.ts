import {beforeEach,it,expect,vi} from 'vitest'
const calls=vi.hoisted(()=>({get:vi.fn(),post:vi.fn()}))
vi.mock('./http',()=>({http:calls}))
import * as api from './containers'
import {listEnvironmentDeployments,listEnvironmentProjects,rollbackEnvironment} from './environments'
const options={headers:{'X-Pipewright-Team':'fixed'},signal:new AbortController().signal,throwOnUnauthorized:true}
beforeEach(()=>{vi.clearAllMocks();calls.get.mockResolvedValue({});calls.post.mockResolvedValue({})})
it.each([
 ['images',()=>api.getServerImages('s',options)],['stats',()=>api.getServerContainerStats('s',options)],['stacks',()=>api.getServerStacks('s',options)],['detail',()=>api.getStackDetail('s','n',options)],['volumes',()=>api.getServerVolumes('s',options)],['networks',()=>api.getServerNetworks('s',options)],['connections',()=>api.getNetworkContainers('s','n',options)],
] as const)('forwards team and abort for %s read',async(_name,work)=>{await work();expect(calls.get.mock.calls[0]?.at(-1)).toBe(options)})
it.each([
 ['pull',()=>api.pullImage('s','i',options)],['image remove',()=>api.removeImage('s','i',false,options)],['volume create',()=>api.createVolume('s','v',options)],['volume remove',()=>api.removeVolume('s','v',options)],['network create',()=>api.createNetwork('s','n',options)],['network remove',()=>api.removeNetwork('s','n',options)],['connect',()=>api.connectNetwork('s','n','c',options)],['disconnect',()=>api.disconnectNetwork('s','n','c',options)],['stack save',()=>api.saveStackCompose('s','n','yaml','path',options)],['stack deploy',()=>api.deployStack('s','n','yaml',options)],['stack action',()=>api.stackAction('s','n','stop','path',options)],['rollback',()=>rollbackEnvironment('p','e',options)],
] as const)('forwards team and abort for %s write',async(_name,work)=>{await work();expect(calls.post.mock.calls[0]?.at(-1)).toBe(options)})
it('forwards context for both environment inventories and aggregate containers',async()=>{await listEnvironmentProjects(options);await listEnvironmentDeployments('p',undefined,options);await api.getAllContainers(options);for(const call of calls.get.mock.calls)expect(call.at(-1)).toEqual(options)})
