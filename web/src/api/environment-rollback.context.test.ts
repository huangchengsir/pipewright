import {expect,it,vi} from 'vitest'
import {http} from './http'
import {rollbackEnvironment} from './environments'
vi.mock('./http',()=>({http:{post:vi.fn()}}))
it('sends both confirmed deployment identities with captured request context',()=>{
 const options={signal:new AbortController().signal,headers:{'X-Pipewright-Team':'team'}}
 const expected={expectedCurrentRunId:'active',expectedTargetRunId:'previous'}
 rollbackEnvironment('project','prod',options,expected)
 expect(http.post).toHaveBeenCalledWith('/api/projects/project/environments/prod/rollback',expected,options)
})
