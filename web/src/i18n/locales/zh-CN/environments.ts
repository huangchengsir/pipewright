export default {
  title: '环境',
  subtitle: '按环境聚合的部署历史与当前活跃版本 · 一键回滚到上一次成功部署',
  project: '项目',
  filterByProject: '按项目筛选',
  selectProject: '请选择项目',

  emptySelectTitle: '请选择一个项目',
  emptySelectDesc: '环境部署历史按项目聚合,先在上方选择项目。',
  emptyTitle: '该项目暂无环境部署历史',
  emptyDesc: '向某环境执行过部署后(webhook 分支映射解析出环境名并完成部署),这里会按环境聚合展示时间线。',

  errLoadTitle: '加载环境部署历史失败',
  errNetwork: '无法连接到服务器,请检查后端是否运行后重试',
  errLoad: '加载环境部署历史失败({status})',
  errLoadRetry: '加载环境部署历史失败,请稍后重试',

  active: '活跃',
  activeVersionTitle: '当前活跃版本',
  noActiveVersion: '无活跃版本',
  noFullSuccessTitle: '尚无全成功部署',
  targetCount: '{n} 台目标机',

  rollback: '回滚',
  rollbackEnabledTitle: '回滚到上一次成功部署',
  rollbackDisabledTitle: '无可回滚的上一次成功部署',
  rollbackTitle: '回滚环境「{env}」',
  rollbackBody:
    '将把该环境回滚到上一次成功部署(运行 {commit} · {when}),即把那次的产物重新部署到原目标机。此操作会触发一次真实部署。',
  rollbackConfirm: '确认回滚',
  rollbackFailedStatus: '回滚失败({status})',
  rollbackFailedRetry: '回滚失败,请稍后重试',

  toastRolledBack: '环境「{env}」已回滚',
  toastRolledBackDetail: '重发产物到 {n} 台目标机',
  toastRollbackPartial: '环境「{env}」回滚部分失败',
  toastRollbackPartialDetail: '{failed}/{total} 台目标机失败',
  toastRollbackFailed: '环境「{env}」回滚失败',

  timelineAria: '{env} 部署历史',
}
