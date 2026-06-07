/**
 * 容器状态展示与生命周期操作的共享元数据。
 * 总览页(计数/筛选)与单机卡片(渲染/操作)共用,避免重复。
 */
import type { ContainerState } from '../api/containers'
import type { ServiceAction } from '../api/servers'

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

const STATE_META: Record<ContainerState, StateMeta> = {
  running: { label: '运行中', tone: 'green', pulse: true },
  paused: { label: '已暂停', tone: 'amber', pulse: false },
  restarting: { label: '重启中', tone: 'amber', pulse: true },
  created: { label: '已创建', tone: 'cyan', pulse: false },
  exited: { label: '已停止', tone: 'faint', pulse: false },
  dead: { label: '异常', tone: 'red', pulse: false },
  unknown: { label: '未知', tone: 'faint', pulse: false },
}

export function stateMeta(s: ContainerState): StateMeta {
  return STATE_META[s] ?? STATE_META.unknown
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
        { action: 'restart', label: '重启', variant: 'default', danger: true },
        { action: 'stop', label: '停止', variant: 'ghost', danger: true },
        { action: 'pause', label: '暂停', variant: 'ghost' },
        { action: 'kill', label: 'Kill', variant: 'ghost', danger: true },
      ]
    case 'paused':
      return [
        { action: 'unpause', label: '恢复', variant: 'default' },
        { action: 'stop', label: '停止', variant: 'ghost', danger: true },
      ]
    case 'restarting':
      return [{ action: 'stop', label: '停止', variant: 'ghost', danger: true }]
    default: // exited / created / dead / unknown
      return [
        { action: 'start', label: '启动', variant: 'default' },
        { action: 'rm', label: '删除', variant: 'ghost', danger: true },
      ]
  }
}

/** 操作按钮的 hover 提示(原生 title):点明各动作机制与区别。 */
export const ACTION_HINTS: Record<ServiceAction, string> = {
  start: '启动已停止的容器(docker start),从头跑新进程。',
  restart: '重启:先优雅停止(SIGTERM,10 秒宽限)再启动(docker restart)。',
  stop: '优雅停止:发 SIGTERM,10 秒内未退再补 SIGKILL(docker stop)。日常停服务用这个,能让程序收尾、落盘。',
  pause: '暂停:cgroup 冻结容器内所有进程(docker pause),内存原样保留、CPU 不再分给它;点「恢复」从断点续跑。不释放内存。',
  unpause: '恢复:解冻已暂停的容器(docker unpause),同一进程从断点继续。',
  kill: '强制 Kill:直接发 SIGKILL 立即终止,不给清理机会(docker kill),可能丢未落盘数据。仅在「停止」卡住时用。',
  rm: '删除容器(docker rm),运行中的需先停止。容器配置移除,挂载的数据卷不受影响。',
}

/** 破坏性操作的二次确认文案。 */
export const DANGER_COPY: Partial<
  Record<ServiceAction, { title: (n: string) => string; body: string; confirmLabel: string }>
> = {
  restart: { title: (n) => `重启容器 ${n}?`, body: '容器将停止后重新启动,期间该服务短暂不可用。', confirmLabel: '确认重启' },
  stop: { title: (n) => `停止容器 ${n}?`, body: '容器将被停止,其提供的服务会中断,直到再次启动。', confirmLabel: '确认停止' },
  kill: { title: (n) => `强制 Kill 容器 ${n}?`, body: '将发送 SIGKILL 立即终止容器进程,可能丢失未落盘数据。', confirmLabel: '强制 Kill' },
  rm: { title: (n) => `删除容器 ${n}?`, body: '将删除该容器(运行中的需先停止)。容器配置随之移除,数据卷不受影响。', confirmLabel: '确认删除' },
}
