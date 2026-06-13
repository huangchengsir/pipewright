export default {
  title: '컨테이너',
  subtitle: '등록된 모든 서버를 가로질러 호스트별로 컨테이너와 이미지를 관리',
  countSummary: '· 총 {total}개 컨테이너 · {running}개 실행 중',
  autoRefresh: '· {n}초마다 자동 새로 고침',

  aiAssistant: '✦ AI 어시스턴트',
  prune: '🧹 정리',
  bulkEnter: '일괄',
  bulkExit: '일괄 종료',
  create: '+ 컨테이너 추가',

  loadingAria: '컨테이너 목록을 불러오는 중',
  errTitle: '컨테이너 목록을 불러오지 못했습니다',
  errConnect: '서버에 연결할 수 없습니다. 백엔드가 실행 중인지 확인한 후 다시 시도하세요.',
  errLoadStatus: '컨테이너 목록을 불러오지 못했습니다({status})',
  errLoadRetry: '컨테이너 목록을 불러오지 못했습니다. 잠시 후 다시 시도하세요.',

  emptyTitle: '아직 등록된 서버가 없습니다',
  emptyDesc: '먼저 「설정 › 서버」에서 대상 서버를 등록하면 해당 서버의 컨테이너와 이미지가 여기에 집계됩니다.',

  kpiTotal: '전체 컨테이너',
  kpiRunning: '실행 중',
  kpiStopped: '중지됨',
  kpiHosts: '컨테이너가 있는 서버',
  kpiStripAria: '컨테이너 집계 통계',

  filterAria: '상태별로 컨테이너 필터링',
  filterAll: '전체',
  filterRunning: '실행 중',
  filterStopped: '중지됨',
  filterPaused: '일시 중지됨',

  searchPlaceholder: '이름 / 이미지로 컨테이너 검색',
  searchAria: '이름 또는 이미지로 컨테이너 검색',
  searchClear: '검색 지우기',

  bulkAria: '일괄 작업',
  bulkSelected: '선택됨',
  bulkSelectedUnit: '개',
  bulkClear: '선택 해제',
  actionStart: '시작',
  actionStop: '중지',
  actionRestart: '재시작',
  actionDelete: '삭제',

  confirmTitle: '컨테이너 {n}개를 일괄 {label}하시겠습니까?',
  confirmBodyRm: '선택한 컨테이너를 삭제합니다(docker rm). 실행 중인 컨테이너는 먼저 중지해야 하며, 그렇지 않으면 삭제에 실패합니다(실패 수에 포함).',
  confirmBodyAction: '선택한 컨테이너 {n}개에 대해 {label} 작업을 실행합니다. 그동안 관련 서비스가 잠시 중단될 수 있습니다.',
  confirmLabel: '{n}개 {label}',

  toastDone: '일괄 {label} 완료',
  toastDoneDetail: '{n}개 성공',
  toastFail: '일괄 {label} 실패',
  toastFailDetail: '{n}개 실패',
  toastPartial: '일괄 {label} 부분 완료',
  toastPartialDetail: '성공 {ok} · 실패 {fail}',

  cardsAria: '서버별 컨테이너 카드',
  aiContextContainer: '(docker 호스트)',
}
