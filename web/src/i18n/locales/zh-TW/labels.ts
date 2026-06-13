export default {
  // ─── 容器狀態 ───
  stateRunning: '運行中',
  statePaused: '已暫停',
  stateRestarting: '重啟中',
  stateCreated: '已建立',
  stateExited: '已停止',
  stateDead: '異常',
  stateUnknown: '未知',

  // ─── 容器生命週期操作按鈕 ───
  actionStart: '啟動',
  actionRestart: '重啟',
  actionStop: '停止',
  actionPause: '暫停',
  actionUnpause: '恢復',
  actionKill: 'Kill',
  actionRm: '刪除',

  // ─── 操作按鈕 hover 提示 ───
  hintStart: '啟動已停止的容器(docker start),從頭跑新行程。',
  hintRestart: '重啟:先優雅停止(SIGTERM,10 秒寬限)再啟動(docker restart)。',
  hintStop: '優雅停止:發 SIGTERM,10 秒內未退再補 SIGKILL(docker stop)。日常停服務用這個,能讓程式收尾、落盤。',
  hintPause: '暫停:cgroup 凍結容器內所有行程(docker pause),記憶體原樣保留、CPU 不再分給它;點「恢復」從斷點續跑。不釋放記憶體。',
  hintUnpause: '恢復:解凍已暫停的容器(docker unpause),同一行程從斷點繼續。',
  hintKill: '強制 Kill:直接發 SIGKILL 立即終止,不給清理機會(docker kill),可能丟未落盤資料。僅在「停止」卡住時用。',
  hintRm: '刪除容器(docker rm),運行中的需先停止。容器設定移除,掛載的資料卷不受影響。',

  // ─── 破壞性操作二次確認 ───
  dangerRestartTitle: '重啟容器 {n}?',
  dangerRestartBody: '容器將停止後重新啟動,期間該服務短暫不可用。',
  dangerRestartConfirm: '確認重啟',
  dangerStopTitle: '停止容器 {n}?',
  dangerStopBody: '容器將被停止,其提供的服務會中斷,直到再次啟動。',
  dangerStopConfirm: '確認停止',
  dangerKillTitle: '強制 Kill 容器 {n}?',
  dangerKillBody: '將發送 SIGKILL 立即終止容器行程,可能遺失未落盤資料。',
  dangerKillConfirm: '強制 Kill',
  dangerRmTitle: '刪除容器 {n}?',
  dangerRmBody: '將刪除該容器(運行中的需先停止)。容器設定隨之移除,資料卷不受影響。',
  dangerRmConfirm: '確認刪除',

  // ─── 參數類型 ───
  paramTypeString: '文字',
  paramTypeChoice: '列舉',
  paramTypeBoolean: '布林',
  paramTypeNumber: '數字',

  // ─── 參數值校驗 ───
  paramRequired: '參數「{label}」為必填',
  paramNotNumber: '參數「{label}」須為數字',
  paramNotBoolean: '參數「{label}」須為 true/false',
  paramNotInChoice: '參數「{label}」不在可選項中',

  // ─── 晉級狀態 ───
  promotionPromoted: '已晉級',
  promotionPending: '待審批',
  promotionRejected: '已拒絕',

  // ─── 環境名校驗 ───
  envNameEmpty: '環境名不能為空',
  envNameInvalid: '環境名只能含字母、數字、連字符、底線',
  envNameTooLong: '環境名不能超過 64 個字元',

  // ─── 並發上限 ───
  concurrencyNotInteger: '並發上限須為整數',
  concurrencyTooSmall: '並發上限不能小於 {min}',
  concurrencyTooLarge: '並發上限不能超過 {max}',
  concurrencyUnlimited: '不限',

  // ─── 終端 ───
  terminalSessionEnded: '終端工作階段已結束',
}
