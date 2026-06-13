export default {
  // ─── 容器状态 ───
  stateRunning: '运行中',
  statePaused: '已暂停',
  stateRestarting: '重启中',
  stateCreated: '已创建',
  stateExited: '已停止',
  stateDead: '异常',
  stateUnknown: '未知',

  // ─── 容器生命周期操作按钮 ───
  actionStart: '启动',
  actionRestart: '重启',
  actionStop: '停止',
  actionPause: '暂停',
  actionUnpause: '恢复',
  actionKill: 'Kill',
  actionRm: '删除',

  // ─── 操作按钮 hover 提示 ───
  hintStart: '启动已停止的容器(docker start),从头跑新进程。',
  hintRestart: '重启:先优雅停止(SIGTERM,10 秒宽限)再启动(docker restart)。',
  hintStop: '优雅停止:发 SIGTERM,10 秒内未退再补 SIGKILL(docker stop)。日常停服务用这个,能让程序收尾、落盘。',
  hintPause: '暂停:cgroup 冻结容器内所有进程(docker pause),内存原样保留、CPU 不再分给它;点「恢复」从断点续跑。不释放内存。',
  hintUnpause: '恢复:解冻已暂停的容器(docker unpause),同一进程从断点继续。',
  hintKill: '强制 Kill:直接发 SIGKILL 立即终止,不给清理机会(docker kill),可能丢未落盘数据。仅在「停止」卡住时用。',
  hintRm: '删除容器(docker rm),运行中的需先停止。容器配置移除,挂载的数据卷不受影响。',

  // ─── 破坏性操作二次确认 ───
  dangerRestartTitle: '重启容器 {n}?',
  dangerRestartBody: '容器将停止后重新启动,期间该服务短暂不可用。',
  dangerRestartConfirm: '确认重启',
  dangerStopTitle: '停止容器 {n}?',
  dangerStopBody: '容器将被停止,其提供的服务会中断,直到再次启动。',
  dangerStopConfirm: '确认停止',
  dangerKillTitle: '强制 Kill 容器 {n}?',
  dangerKillBody: '将发送 SIGKILL 立即终止容器进程,可能丢失未落盘数据。',
  dangerKillConfirm: '强制 Kill',
  dangerRmTitle: '删除容器 {n}?',
  dangerRmBody: '将删除该容器(运行中的需先停止)。容器配置随之移除,数据卷不受影响。',
  dangerRmConfirm: '确认删除',

  // ─── 参数类型 ───
  paramTypeString: '文本',
  paramTypeChoice: '枚举',
  paramTypeBoolean: '布尔',
  paramTypeNumber: '数字',

  // ─── 参数值校验 ───
  paramRequired: '参数「{label}」为必填',
  paramNotNumber: '参数「{label}」须为数字',
  paramNotBoolean: '参数「{label}」须为 true/false',
  paramNotInChoice: '参数「{label}」不在可选项中',

  // ─── 晋级状态 ───
  promotionPromoted: '已晋级',
  promotionPending: '待审批',
  promotionRejected: '已拒绝',

  // ─── 环境名校验 ───
  envNameEmpty: '环境名不能为空',
  envNameInvalid: '环境名只能含字母、数字、连字符、下划线',
  envNameTooLong: '环境名不能超过 64 个字符',

  // ─── 并发上限 ───
  concurrencyNotInteger: '并发上限须为整数',
  concurrencyTooSmall: '并发上限不能小于 {min}',
  concurrencyTooLarge: '并发上限不能超过 {max}',
  concurrencyUnlimited: '不限',

  // ─── 终端 ───
  terminalSessionEnded: '终端会话已结束',
}
