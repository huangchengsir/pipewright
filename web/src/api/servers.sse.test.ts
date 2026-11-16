import {afterEach,it,expect,vi} from 'vitest'
import {subscribeServerLogs} from './servers'
afterEach(()=>vi.unstubAllGlobals())
it('uses team header and cancels the reader without delivering queued bytes',async()=>{
 let stream!:ReadableStreamDefaultController<Uint8Array>
 const fetcher=vi.fn().mockResolvedValue(new Response(new ReadableStream({start(c){stream=c}})))
 vi.stubGlobal('fetch',fetcher)
 const onLine=vi.fn(),controller=new AbortController()
 const stop=subscribeServerLogs('s1',{source:'docker',target:'app'},{onLine},{headers:{'X-Pipewright-Team':'a'},signal:controller.signal})
 await vi.waitFor(()=>expect(fetcher).toHaveBeenCalledOnce())
 stream.enqueue(new TextEncoder().encode('event: logline\ndata: {"text":"first"}\n\n'))
 await vi.waitFor(()=>expect(onLine).toHaveBeenCalledWith('first'))
 stream.enqueue(new TextEncoder().encode('event: logline\ndata: {"text":"late"}\n\n'));controller.abort()
 await new Promise(r=>setTimeout(r,0))
 expect(onLine).toHaveBeenCalledTimes(1);expect(fetcher.mock.calls[0]![1].headers).toEqual({'X-Pipewright-Team':'a'});expect(fetcher.mock.calls[0]![1].signal.aborted).toBe(true);stop()
})
it.each([401,403])('does not reconnect after authorization response %s',async(status)=>{const fetcher=vi.fn().mockResolvedValue(new Response('',{status}));vi.stubGlobal('fetch',fetcher);const onError=vi.fn();const stop=subscribeServerLogs('s1',{source:'file',target:'/log'},{onLine:vi.fn(),onError},{headers:{'X-Pipewright-Team':'a'}});await vi.waitFor(()=>expect(onError).toHaveBeenCalledOnce());expect(fetcher).toHaveBeenCalledOnce();stop()})
it('does not connect an already aborted subscription',()=>{const fetcher=vi.fn();vi.stubGlobal('fetch',fetcher);const c=new AbortController();c.abort();subscribeServerLogs('s1',{source:'file',target:'/log'},{onLine:vi.fn()},{headers:{'X-Pipewright-Team':'a'},signal:c.signal});expect(fetcher).not.toHaveBeenCalled()})
