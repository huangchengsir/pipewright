export default {
  // ─── 컨테이너 상태 ───
  stateRunning: '실행 중',
  statePaused: '일시 중지',
  stateRestarting: '재시작 중',
  stateCreated: '생성됨',
  stateExited: '중지됨',
  stateDead: '비정상',
  stateUnknown: '알 수 없음',

  // ─── 컨테이너 수명 주기 작업 버튼 ───
  actionStart: '시작',
  actionRestart: '재시작',
  actionStop: '중지',
  actionPause: '일시 중지',
  actionUnpause: '재개',
  actionKill: 'Kill',
  actionRm: '삭제',

  // ─── 작업 버튼 호버 힌트 ───
  hintStart: '중지된 컨테이너를 시작합니다(docker start). 새 프로세스를 처음부터 실행합니다.',
  hintRestart: '재시작: 먼저 정상적으로 중지한 뒤(SIGTERM, 10초 유예) 시작합니다(docker restart).',
  hintStop: '정상 중지: SIGTERM을 보내고 10초 내에 종료되지 않으면 SIGKILL을 추가로 보냅니다(docker stop). 일상적인 서비스 중지에는 이것을 사용하여 프로그램이 마무리하고 디스크에 기록할 수 있게 합니다.',
  hintPause: '일시 중지: cgroup으로 컨테이너 내 모든 프로세스를 동결합니다(docker pause). 메모리는 그대로 유지되고 CPU는 더 이상 할당되지 않습니다. "재개"를 누르면 중단점부터 이어서 실행합니다. 메모리를 해제하지 않습니다.',
  hintUnpause: '재개: 일시 중지된 컨테이너를 동결 해제합니다(docker unpause). 동일한 프로세스가 중단점부터 계속됩니다.',
  hintKill: '강제 Kill: SIGKILL을 직접 보내 정리 기회 없이 즉시 종료합니다(docker kill). 기록되지 않은 데이터가 손실될 수 있습니다. "중지"가 멈췄을 때만 사용하세요.',
  hintRm: '컨테이너를 삭제합니다(docker rm). 실행 중인 경우 먼저 중지해야 합니다. 컨테이너 설정은 제거되지만 마운트된 데이터 볼륨은 영향을 받지 않습니다.',

  // ─── 파괴적 작업 확인 ───
  dangerRestartTitle: '컨테이너 {n}을(를) 재시작하시겠습니까?',
  dangerRestartBody: '컨테이너가 중지된 후 다시 시작되며, 그동안 해당 서비스를 잠시 사용할 수 없습니다.',
  dangerRestartConfirm: '재시작 확인',
  dangerStopTitle: '컨테이너 {n}을(를) 중지하시겠습니까?',
  dangerStopBody: '컨테이너가 중지되어 제공하는 서비스가 다시 시작될 때까지 중단됩니다.',
  dangerStopConfirm: '중지 확인',
  dangerKillTitle: '컨테이너 {n}을(를) 강제 Kill하시겠습니까?',
  dangerKillBody: 'SIGKILL을 보내 컨테이너 프로세스를 즉시 종료합니다. 기록되지 않은 데이터가 손실될 수 있습니다.',
  dangerKillConfirm: '강제 Kill',
  dangerRmTitle: '컨테이너 {n}을(를) 삭제하시겠습니까?',
  dangerRmBody: '이 컨테이너를 삭제합니다(실행 중인 경우 먼저 중지해야 함). 설정도 함께 제거되지만 데이터 볼륨은 영향을 받지 않습니다.',
  dangerRmConfirm: '삭제 확인',

  // ─── 매개변수 유형 ───
  paramTypeString: '텍스트',
  paramTypeChoice: '열거',
  paramTypeBoolean: '불리언',
  paramTypeNumber: '숫자',

  // ─── 매개변수 값 검증 ───
  paramRequired: '매개변수 "{label}"은(는) 필수입니다',
  paramNotNumber: '매개변수 "{label}"은(는) 숫자여야 합니다',
  paramNotBoolean: '매개변수 "{label}"은(는) true/false여야 합니다',
  paramNotInChoice: '매개변수 "{label}"은(는) 선택 항목에 없습니다',

  // ─── 승격 상태 ───
  promotionPromoted: '승격됨',
  promotionPending: '승인 대기',
  promotionRejected: '거부됨',

  // ─── 환경 이름 검증 ───
  envNameEmpty: '환경 이름은 비워 둘 수 없습니다',
  envNameInvalid: '환경 이름에는 문자, 숫자, 하이픈, 밑줄만 사용할 수 있습니다',
  envNameTooLong: '환경 이름은 64자를 초과할 수 없습니다',

  // ─── 동시 실행 한도 ───
  concurrencyNotInteger: '동시 실행 한도는 정수여야 합니다',
  concurrencyTooSmall: '동시 실행 한도는 {min}보다 작을 수 없습니다',
  concurrencyTooLarge: '동시 실행 한도는 {max}을(를) 초과할 수 없습니다',
  concurrencyUnlimited: '무제한',

  // ─── 터미널 ───
  terminalSessionEnded: '터미널 세션이 종료되었습니다',
}
