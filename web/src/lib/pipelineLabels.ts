/**
 * Localize backend-generated default stage/job names.
 *
 * The backend seeds new projects with Chinese default names (`流水线源`, `构建`,
 * job `Gitee 源`). Those are stored as data, so we localize them at *display*
 * time: if a name matches a known default token, render the localized label by
 * kind; user-renamed stages/jobs (anything else) render verbatim.
 *
 * Uses the global i18n `t` (reactive in Composition mode), so labels follow the
 * active UI language without re-fetching.
 */
import { t } from '../i18n'

const DEFAULT_NAME_KEYS: Record<string, string> = {
  流水线源: 'pipelineDefaults.sourceStage',
  构建: 'pipelineDefaults.buildStage',
  部署: 'pipelineDefaults.deployStage',
  通知: 'pipelineDefaults.notifyStage',
  'Gitee 源': 'pipelineDefaults.gitSourceJob',
}

/** Localize a stage/job name iff it's a known backend default; else return as-is. */
export function localizeName(name: string): string {
  const key = DEFAULT_NAME_KEYS[name?.trim()]
  return key ? t(key) : name
}
