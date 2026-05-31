import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { importPipeline, type PipelineDTO } from './pipeline'
import { HttpError } from './http'

function jsonResponse(status: number, body: unknown, ok = status < 400): Response {
  return {
    status,
    ok,
    headers: new Headers({ 'content-type': 'application/json' }),
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as unknown as Response
}

const dto: PipelineDTO = {
  stages: [{ id: 's1', name: '源', kind: 'source', jobs: [] }],
  yaml: 'version: 1\n',
  status: 'draft',
  updatedAt: '2026-05-31T00:00:00Z',
}

describe('importPipeline (FR-8-12)', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('POSTs to /pipeline/import with save=false by default (preview)', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, dto))
    const result = await importPipeline('p1', 'version: 1\nstages: []\n')
    expect(result).toEqual(dto)

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/projects/p1/pipeline/import')
    expect(init.method).toBe('POST')
    const sent = JSON.parse(init.body as string)
    expect(sent.save).toBe(false)
    expect(sent.yaml).toContain('stages')
  })

  it('passes save=true through to persist', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, dto))
    await importPipeline('p1', 'yaml', true)
    const [, init] = fetchMock.mock.calls[0]
    expect(JSON.parse(init.body as string).save).toBe(true)
  })

  it('throws HttpError with the server code on a 422 validation failure', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(422, { error: { code: 'invalid_stage', message: '阶段配置不合法' } }),
    )
    await expect(importPipeline('p1', 'bad')).rejects.toBeInstanceOf(HttpError)
    try {
      await importPipeline('p1', 'bad')
    } catch (err) {
      expect(err).toBeInstanceOf(HttpError)
      expect((err as HttpError).apiError?.code).toBe('invalid_stage')
      expect((err as HttpError).status).toBe(422)
    }
  })
})
