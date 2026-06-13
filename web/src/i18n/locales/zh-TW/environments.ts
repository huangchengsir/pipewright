export default {
  title: '環境',
  subtitle: '按環境聚合的部署歷史與目前活躍版本 · 一鍵回滾到上一次成功部署',
  project: '專案',
  filterByProject: '依專案篩選',
  selectProject: '請選擇專案',

  emptySelectTitle: '請選擇一個專案',
  emptySelectDesc: '環境部署歷史依專案聚合,請先在上方選擇專案。',
  emptyTitle: '該專案尚無環境部署歷史',
  emptyDesc: '向某環境執行過部署後(webhook 分支對應解析出環境名並完成部署),這裡會依環境聚合顯示時間軸。',

  errLoadTitle: '載入環境部署歷史失敗',
  errNetwork: '無法連線到伺服器,請確認後端是否執行後重試',
  errLoad: '載入環境部署歷史失敗({status})',
  errLoadRetry: '載入環境部署歷史失敗,請稍後重試',

  active: '活躍',
  activeVersionTitle: '目前活躍版本',
  noActiveVersion: '無活躍版本',
  noFullSuccessTitle: '尚無全部成功的部署',
  targetCount: '{n} 台目標機',

  rollback: '回滾',
  rollbackEnabledTitle: '回滾到上一次成功部署',
  rollbackDisabledTitle: '無可回滾的上一次成功部署',
  rollbackTitle: '回滾環境「{env}」',
  rollbackBody:
    '將把該環境回滾到上一次成功部署(執行 {commit} · {when}),即把那次的產物重新部署到原目標機。此操作會觸發一次真實部署。',
  rollbackConfirm: '確認回滾',
  rollbackFailedStatus: '回滾失敗({status})',
  rollbackFailedRetry: '回滾失敗,請稍後重試',

  toastRolledBack: '環境「{env}」已回滾',
  toastRolledBackDetail: '重新發送產物到 {n} 台目標機',
  toastRollbackPartial: '環境「{env}」回滾部分失敗',
  toastRollbackPartialDetail: '{failed}/{total} 台目標機失敗',
  toastRollbackFailed: '環境「{env}」回滾失敗',

  timelineAria: '{env} 部署歷史',
}
