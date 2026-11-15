import {it,expect,vi} from 'vitest'
const calls=vi.hoisted(()=>({get:vi.fn(),post:vi.fn()}))
vi.mock('./http',()=>({http:calls}))
import {getSystemDf,systemPrune} from './containers'
it('forwards immutable request context for disk usage and cleanup',async()=>{
 const options={headers:{'X-Pipewright-Team':'fixed-team'},signal:new AbortController().signal,throwOnUnauthorized:true}
 await getSystemDf('server-a',options);await systemPrune('server-a','images',options)
 expect(calls.get).toHaveBeenCalledWith('/api/servers/server-a/system/df',options)
 expect(calls.post).toHaveBeenCalledWith('/api/servers/server-a/system/prune',{scope:'images'},options)
})