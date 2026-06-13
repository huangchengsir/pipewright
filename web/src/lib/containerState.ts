/**
 * 容器状态展示与生命周期操作的共享元数据。
 * 总览页(计数/筛选)与单机卡片(渲染/操作)共用,避免重复。
 */
import type { ContainerState } from '../api/containers'
import type { ServiceAction } from '../api/servers'
import { t } from '../i18n'

/** 六态归并三桶,供筛选与计数。 */
export type StateBucket = 'running' | 'paused' | 'stopped'

export function stateBucket(s: ContainerState): StateBucket {
  if (s === 'running' || s === 'restarting') return 'running'
  if (s === 'paused') return 'paused'
  return 'stopped'
}

export interface StateMeta {
  label: string
  tone: 'green' | 'amber' | 'cyan' | 'red' | 'faint'
  pulse: boolean
}

// tone/pulse 是稳定的视觉元数据;label 在访问时经 t() 解析,保证随语言切换更新。
const STATE_VISUAL: Record<ContainerState, { labelKey: string; tone: StateMeta['tone']; pulse: boolean }> = {
  running: { labelKey: 'labels.stateRunning', tone: 'green', pulse: true },
  paused: { labelKey: 'labels.statePaused', tone: 'amber', pulse: false },
  restarting: { labelKey: 'labels.stateRestarting', tone: 'amber', pulse: true },
  created: { labelKey: 'labels.stateCreated', tone: 'cyan', pulse: false },
  exited: { labelKey: 'labels.stateExited', tone: 'faint', pulse: false },
  dead: { labelKey: 'labels.stateDead', tone: 'red', pulse: false },
  unknown: { labelKey: 'labels.stateUnknown', tone: 'faint', pulse: false },
}

export function stateMeta(s: ContainerState): StateMeta {
  const v = STATE_VISUAL[s] ?? STATE_VISUAL.unknown
  return { label: t(v.labelKey), tone: v.tone, pulse: v.pulse }
}

export function shortId(id: string): string {
  return id.length > 12 ? id.slice(0, 12) : id
}

export interface ActionSpec {
  action: ServiceAction
  label: string
  variant: 'default' | 'ghost' | 'danger'
  /** 需二次确认。 */
  danger?: boolean
}

/** 据容器状态给出可用生命周期操作集(顺序即展示顺序)。 */
export function actionsFor(state: ContainerState): ActionSpec[] {
  switch (state) {
    case 'running':
      return [
        { action: 'restart', label: t('labels.actionRestart'), variant: 'default', danger: true },
        { action: 'stop', label: t('labels.actionStop'), variant: 'ghost', danger: true },
        { action: 'pause', label: t('labels.actionPause'), variant: 'ghost' },
        { action: 'kill', label: t('labels.actionKill'), variant: 'ghost', danger: true },
      ]
    case 'paused':
      return [
        { action: 'unpause', label: t('labels.actionUnpause'), variant: 'default' },
        { action: 'stop', label: t('labels.actionStop'), variant: 'ghost', danger: true },
      ]
    case 'restarting':
      return [{ action: 'stop', label: t('labels.actionStop'), variant: 'ghost', danger: true }]
    default: // exited / created / dead / unknown
      return [
        { action: 'start', label: t('labels.actionStart'), variant: 'default' },
        { action: 'rm', label: t('labels.actionRm'), variant: 'ghost', danger: true },
      ]
  }
}

/**
 * 操作按钮的 hover 提示(原生 title):点明各动作机制与区别。
 * 用 getter 实现,使 t() 在访问时求值 → 随语言切换实时更新。
 */
export const ACTION_HINTS: Record<ServiceAction, string> = {
  get start() {
    return t('labels.hintStart')
  },
  get restart() {
    return t('labels.hintRestart')
  },
  get stop() {
    return t('labels.hintStop')
  },
  get pause() {
    return t('labels.hintPause')
  },
  get unpause() {
    return t('labels.hintUnpause')
  },
  get kill() {
    return t('labels.hintKill')
  },
  get rm() {
    return t('labels.hintRm')
  },
}

/**
 * 破坏性操作的二次确认文案。
 * body / confirmLabel 用 getter,title 是函数 → t() 均在访问时求值,保持随语言切换更新。
 */
export const DANGER_COPY: Partial<
  Record<ServiceAction, { title: (n: string) => string; body: string; confirmLabel: string }>
> = {
  restart: {
    title: (n) => t('labels.dangerRestartTitle', { n }),
    get body() {
      return t('labels.dangerRestartBody')
    },
    get confirmLabel() {
      return t('labels.dangerRestartConfirm')
    },
  },
  stop: {
    title: (n) => t('labels.dangerStopTitle', { n }),
    get body() {
      return t('labels.dangerStopBody')
    },
    get confirmLabel() {
      return t('labels.dangerStopConfirm')
    },
  },
  kill: {
    title: (n) => t('labels.dangerKillTitle', { n }),
    get body() {
      return t('labels.dangerKillBody')
    },
    get confirmLabel() {
      return t('labels.dangerKillConfirm')
    },
  },
  rm: {
    title: (n) => t('labels.dangerRmTitle', { n }),
    get body() {
      return t('labels.dangerRmBody')
    },
    get confirmLabel() {
      return t('labels.dangerRmConfirm')
    },
  },
}
