import { beforeEach, describe, expect, it, vi } from 'vitest'
import { http } from './http'
import { bindConfigRevision, configRevisionOf } from './configRevision'
import { saveCron } from './cron'
import { saveConcurrency } from './concurrency'
import { saveChain } from './chain'
import { getParameters, saveParameters } from './parameters'
import { saveRunner } from './runner'
import { saveEnvironments } from './promotion'
import { resetSecret } from './triggers'
import { importPipeline } from './pipeline'
import { applyTemplate } from './templates'

vi.mock('./http', () => ({ http: { get: vi.fn(), put: vi.fn(), post: vi.fn() } }))
describe('configuration write preconditions', () => {
  beforeEach(() => { vi.clearAllMocks(); vi.mocked(http.put).mockResolvedValue({ parameters: [] }); vi.mocked(http.post).mockResolvedValue({}) })
  it.each([
    ['cron', () => saveCron('p', { enabled: false, expression: '', branch: '' }, 'cfg-7'), 'put', '/api/projects/p/cron'],
    ['concurrency', () => saveConcurrency('p', { maxConcurrent: 1 }, 'cfg-7'), 'put', '/api/projects/p/concurrency'],
    ['chain', () => saveChain('p', { downstream: [] }, 'cfg-7'), 'put', '/api/projects/p/chain'],
    ['parameters', () => saveParameters('p', [], 'cfg-7'), 'put', '/api/projects/p/parameters'],
    ['runner', () => saveRunner('p', '', 'cfg-7'), 'put', '/api/projects/p/runner'],
    ['environments', () => saveEnvironments('p', { environments: [] }, 'cfg-7'), 'put', '/api/projects/p/environments'],
    ['secret reset', () => resetSecret('p', 'cfg-7'), 'post', '/api/projects/p/trigger/secret/reset'],
    ['saved import', () => importPipeline('p', 'version: 1', true, 'cfg-7'), 'post', '/api/projects/p/pipeline/import'],
    ['template apply', () => applyTemplate('p', 'template-1', 'cfg-7'), 'post', '/api/projects/p/pipeline/apply-template'],
  ] as const)('sends the fixed editor baseline for %s', async (_name, invoke, method, url) => {
    await invoke()
    const calls = vi.mocked(http[method]).mock.calls
    expect(calls).toHaveLength(1)
    expect(calls[0][0]).toBe(url)
    expect(new Headers(calls[0][2]?.headers).get('If-Match')).toBe('cfg-7')
    expect(http.get).not.toHaveBeenCalled()
  })
  it('retains header metadata when a parameters response is unwrapped into an array', async () => {
    vi.mocked(http.get).mockResolvedValue(bindConfigRevision({ parameters: [] }, 'cfg-8'))
    expect(configRevisionOf(await getParameters('p'))).toBe('cfg-8')
    vi.mocked(http.put).mockResolvedValue(bindConfigRevision({ parameters: [] }, 'cfg-9'))
    expect(configRevisionOf(await saveParameters('p', [], 'cfg-8'))).toBe('cfg-9')
  })
  it('keeps YAML preview read-only without write preconditions', async () => {
    await importPipeline('p', 'version: 1', false, 'cfg-7')
    expect(http.post).toHaveBeenCalledExactlyOnceWith('/api/projects/p/pipeline/import', { yaml: 'version: 1', save: false })
  })
})

it.each([false,true])('keeps fixed team, abort signal and If-Match with Headers input (cron=%s)',async cron=>{
 const signal=new AbortController().signal;const headers=new Headers({'X-Pipewright-Team':'fixed','If-Match':'old'});
 if(cron)await saveCron('p',{enabled:false,expression:'',branch:''},'cfg-7',{headers,signal});
 else await saveConcurrency('p',{maxConcurrent:1},'cfg-7',{headers,signal});
 const options=vi.mocked(http.put).mock.calls.at(-1)![2]!;expect(options.signal).toBe(signal);expect(options.throwOnUnauthorized).toBe(true);const actual=new Headers(options.headers);expect(actual.get('X-Pipewright-Team')).toBe('fixed');expect(actual.get('If-Match')).toBe('cfg-7');expect(headers.get('If-Match')).toBe('old');
});
