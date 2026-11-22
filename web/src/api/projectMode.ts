import { http, type HttpOptions } from './http'

export const PROJECT_MODES = ['single', 'multi'] as const
export type ProjectMode = (typeof PROJECT_MODES)[number]

const projectModePath = '/api/account/project-mode'

function readMode(response: { mode?: unknown } | undefined): ProjectMode {
  if (response?.mode === 'single' || response?.mode === 'multi') return response.mode
  throw new Error('Invalid project mode response')
}

export async function getProjectMode(options: HttpOptions): Promise<ProjectMode> {
  return readMode(await http.get<{ mode: unknown }>(projectModePath, options))
}

export async function putProjectMode(mode: ProjectMode, options: HttpOptions): Promise<ProjectMode> {
  return readMode(await http.put<{ mode: unknown }>(projectModePath, { mode }, options))
}
