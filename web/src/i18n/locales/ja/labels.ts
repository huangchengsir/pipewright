export default {
  // ─── コンテナの状態 ───
  stateRunning: '実行中',
  statePaused: '一時停止',
  stateRestarting: '再起動中',
  stateCreated: '作成済み',
  stateExited: '停止済み',
  stateDead: '異常',
  stateUnknown: '不明',

  // ─── コンテナのライフサイクル操作ボタン ───
  actionStart: '起動',
  actionRestart: '再起動',
  actionStop: '停止',
  actionPause: '一時停止',
  actionUnpause: '再開',
  actionKill: 'Kill',
  actionRm: '削除',

  // ─── 操作ボタンのホバーヒント ───
  hintStart: '停止中のコンテナを起動します（docker start）。新しいプロセスを最初から実行します。',
  hintRestart: '再起動：まず正常に停止し（SIGTERM、10 秒の猶予）、その後起動します（docker restart）。',
  hintStop: '正常停止：SIGTERM を送信し、10 秒以内に終了しなければ SIGKILL を補います（docker stop）。日常的なサービス停止はこれを使い、プログラムが後始末してディスクに書き込めるようにします。',
  hintPause: '一時停止：cgroup でコンテナ内の全プロセスを凍結します（docker pause）。メモリはそのまま保持され、CPU は割り当てられなくなります。「再開」を押すと中断点から続行します。メモリは解放しません。',
  hintUnpause: '再開：一時停止中のコンテナを解凍します（docker unpause）。同じプロセスが中断点から続行します。',
  hintKill: '強制 Kill：SIGKILL を直接送信して即座に終了し、後始末の機会を与えません（docker kill）。未書き込みのデータが失われる可能性があります。「停止」が固まったときのみ使用してください。',
  hintRm: 'コンテナを削除します（docker rm）。実行中のものは先に停止する必要があります。コンテナの設定は削除されますが、マウントされたデータボリュームには影響しません。',

  // ─── 破壊的操作の確認 ───
  dangerRestartTitle: 'コンテナ {n} を再起動しますか？',
  dangerRestartBody: 'コンテナは停止後に再起動され、その間そのサービスは一時的に利用できなくなります。',
  dangerRestartConfirm: '再起動を確認',
  dangerStopTitle: 'コンテナ {n} を停止しますか？',
  dangerStopBody: 'コンテナは停止され、提供しているサービスは再起動するまで中断されます。',
  dangerStopConfirm: '停止を確認',
  dangerKillTitle: 'コンテナ {n} を強制 Kill しますか？',
  dangerKillBody: 'SIGKILL を送信してコンテナのプロセスを即座に終了します。未書き込みのデータが失われる可能性があります。',
  dangerKillConfirm: '強制 Kill',
  dangerRmTitle: 'コンテナ {n} を削除しますか？',
  dangerRmBody: 'このコンテナを削除します（実行中のものは先に停止する必要があります）。設定もともに削除されますが、データボリュームには影響しません。',
  dangerRmConfirm: '削除を確認',

  // ─── パラメータの型 ───
  paramTypeString: 'テキスト',
  paramTypeChoice: '列挙',
  paramTypeBoolean: '真偽値',
  paramTypeNumber: '数値',

  // ─── パラメータ値の検証 ───
  paramRequired: 'パラメータ「{label}」は必須です',
  paramNotNumber: 'パラメータ「{label}」は数値である必要があります',
  paramNotBoolean: 'パラメータ「{label}」は true/false である必要があります',
  paramNotInChoice: 'パラメータ「{label}」は選択肢に含まれていません',

  // ─── 昇格ステータス ───
  promotionPromoted: '昇格済み',
  promotionPending: '承認待ち',
  promotionRejected: '却下',

  // ─── 環境名の検証 ───
  envNameEmpty: '環境名を空にすることはできません',
  envNameInvalid: '環境名には英数字、ハイフン、アンダースコアのみ使用できます',
  envNameTooLong: '環境名は 64 文字を超えることはできません',

  // ─── 同時実行数の上限 ───
  concurrencyNotInteger: '同時実行数の上限は整数である必要があります',
  concurrencyTooSmall: '同時実行数の上限は {min} より小さくすることはできません',
  concurrencyTooLarge: '同時実行数の上限は {max} を超えることはできません',
  concurrencyUnlimited: '無制限',

  // ─── ターミナル ───
  terminalSessionEnded: 'ターミナルセッションが終了しました',
}
