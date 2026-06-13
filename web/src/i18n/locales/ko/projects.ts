export default {
  // ─── page header ───────────────────────────────────────────────
  title: '프로젝트',
  subtitle: '관리 중인 Gitee 저장소 — 각 프로젝트는 하나의 파이프라인 설정과 배포 대상에 매핑됩니다',
  newProject: '새 프로젝트',
  retry: '다시 시도',

  // ─── toolbar / search / filter ─────────────────────────────────
  searchFilterAria: '프로젝트 검색 및 필터',
  searchPlaceholder: '프로젝트 이름, 저장소 URL, 브랜치로 검색…',
  searchAria: '프로젝트 검색',
  statusFilterAria: '상태로 필터',
  statusAll: '모든 상태',

  // ─── list states ───────────────────────────────────────────────
  loading: '불러오는 중',
  emptyTitle: '아직 프로젝트가 없습니다',
  emptyHint: '첫 Gitee 저장소를 연결하면 파이프라인을 설정하고 대상 서버에 배포할 수 있습니다.',
  noMatchTitle: '일치하는 프로젝트가 없습니다',
  noMatchHint: '검색어나 상태 필터 조건을 조정한 후 다시 시도하세요.',
  clearFilter: '필터 지우기',
  resultCount: '프로젝트 {n}개',
  resultCountTotal: '(전체 {total}개)',

  // ─── project card ──────────────────────────────────────────────
  runStatusAria: '실행 상태: {status}',
  lastRun: '마지막 실행',
  noRun: '실행 기록 없음',
  targetServers: '대상 서버',
  notBound: '미연결',
  credential: '저장소 자격 증명',
  credentialRefTitle: '자격 증명 참조(평문 아님)',
  updatedAt: '{time} 업데이트됨',

  // ─── card actions ──────────────────────────────────────────────
  actionRunTitle: '수동으로 실행 트리거 · {name}',
  actionRunAria: '프로젝트 {name}의 파이프라인 실행을 수동으로 트리거',
  actionRenameTitle: '{name} 이름 변경',
  actionRenameAria: '프로젝트 {name} 이름 변경',
  actionCodeTitle: '코드 보기 · {name}',
  actionCodeAria: '프로젝트 {name}의 코드 보기',
  actionPipelineTitle: '파이프라인 설정 · {name}',
  actionPipelineAria: '프로젝트 {name}의 파이프라인 설정',
  actionDeleteTitle: '{name} 삭제',
  actionDeleteAria: '프로젝트 {name} 삭제',

  // ─── shared modal ──────────────────────────────────────────────
  closeDialog: '대화상자 닫기',
  cancel: '취소',
  save: '저장',
  saving: '저장 중…',

  // ─── trigger modal ─────────────────────────────────────────────
  triggerDialogAria: '수동 트리거 · {name}',
  triggerTitle: '수동으로 실행 트리거',
  triggerSub: '{name} · 브랜치를 지정하고 즉시 파이프라인 실행을 생성합니다',
  branch: '브랜치',
  branchHint: '(선택, 비워 두면 프로젝트 기본 브랜치 사용)',
  commit: 'Commit',
  commitHint: '(선택, 비워 두면 브랜치 HEAD 사용)',
  commitPlaceholder: '예: a3f1c2d',
  params: '매개변수',
  paramsHintTyped: '(정의에 따라 입력하면 환경 변수로 파이프라인에 주입됩니다)',
  paramsHintFree: '(선택, 환경 변수로 파이프라인에 주입됩니다)',
  triggering: '트리거 중…',
  runNow: '지금 실행',

  // ─── create modal ──────────────────────────────────────────────
  createSub: 'Gitee 저장소를 연결하고 저장소 자격 증명을 바인딩합니다',
  fieldName: '프로젝트 이름',
  fieldNamePlaceholder: '예: acme-web',
  fieldRepo: '저장소 URL',
  fieldCredHint: '(Git 토큰 유형만 표시되며 평문은 포함되지 않습니다)',
  credLoading: '자격 증명 불러오는 중…',
  credSelect: 'Git 토큰 자격 증명 선택',
  credEmptyPre: 'Git 토큰 자격 증명이 아직 없습니다. 먼저',
  credVaultLink: '자격 증명 볼트',
  credEmptyPost: '에서 추가하세요.',
  fieldDefaultBranch: '기본 브랜치',
  fieldDefaultBranchHint: '(선택, 비워 두면 연결 테스트로 자동 감지됩니다)',
  testConnection: '연결 테스트',
  testing: '테스트 중…',
  testOk: '연결 성공',
  testDetectedBranch: '· 기본 브랜치 {branch}',
  creating: '생성 중…',
  createSubmit: '프로젝트 생성',

  // ─── rename modal ──────────────────────────────────────────────
  renameTitle: '프로젝트 이름 변경',
  renameSub: '프로젝트의 표시 이름을 변경합니다',

  // ─── delete modal ──────────────────────────────────────────────
  deleteDialogAria: '프로젝트 삭제 확인',
  deleteTitle: '프로젝트 삭제',
  deleteSub: '이 작업은 되돌릴 수 없습니다',
  deleteConfirmPre: '프로젝트',
  deleteConfirmPost: '을(를) 영구적으로 삭제하시겠습니까? 파이프라인 설정, 실행 기록, 자격 증명 참조 관계가 모두 정리됩니다.',
  deleting: '삭제 중…',
  confirmDelete: '삭제 확인',

  // ─── error messages ────────────────────────────────────────────
  errNetwork: '서버에 연결할 수 없습니다. 백엔드가 실행 중인지 확인한 후 다시 시도하세요.',
  errNetworkRetry: '서버에 연결할 수 없습니다. 잠시 후 다시 시도하세요.',
  errLoadNetwork: '서버에 연결할 수 없습니다. 백엔드가 실행 중인지 확인한 후 다시 시도하세요',
  errLoadStatus: '프로젝트를 불러오지 못했습니다({status})',
  errLoadRetry: '프로젝트를 불러오지 못했습니다. 잠시 후 다시 시도하세요',
  errNameRequired: '프로젝트 이름을 입력하세요',
  errRepoRequired: '저장소 URL을 입력하세요',
  errRepoFormat: '저장소 URL 형식이 올바르지 않습니다. https:// 또는 git@ 로 시작해야 합니다',
  errCredRequired: '저장소 자격 증명을 선택하세요',
  errRepoFirst: '먼저 저장소 URL을 입력하세요',
  errCredFirst: '먼저 저장소 자격 증명을 선택하세요',
  errNameEmpty: '프로젝트 이름은 비워 둘 수 없습니다',

  testErrCredential: '자격 증명 오류: Gitee 액세스 토큰이 유효한지 확인하고 자격 증명 볼트에서 업데이트하세요.',
  testErrUnreachable: '저장소에 연결할 수 없습니다: URL이 올바르고 저장소가 존재하며 접근 가능한지 확인하세요.',
  testErrVault: '볼트에 master key가 설정되지 않아 자격 증명을 읽을 수 없습니다.',
  testErrStatus: '연결 테스트에 실패했습니다({status})',
  testErrRetry: '연결 테스트에 실패했습니다. 잠시 후 다시 시도하세요.',

  createErrCredField: '자격 증명 오류: 액세스 토큰이 유효한지 확인하세요',
  createErrCredBanner: '자격 증명 검증에 실패했습니다. 자격 증명을 변경하거나 자격 증명 볼트에서 업데이트하세요.',
  createErrRepoField: '저장소에 연결할 수 없습니다: URL이 올바르고 접근 가능한지 확인하세요',
  createErrRepoBanner: '저장소 URL에 연결할 수 없어 생성에 실패했습니다.',
  createErrVault: '볼트에 master key가 설정되지 않아 프로젝트를 저장할 수 없습니다.',
  createErrStatus: '생성에 실패했습니다({status})',
  createErrRetry: '생성에 실패했습니다. 잠시 후 다시 시도하세요.',

  renameErrStatus: '이름 변경에 실패했습니다({status})',
  renameErrRetry: '이름 변경에 실패했습니다. 잠시 후 다시 시도하세요.',

  deleteErrStatus: '삭제에 실패했습니다({status})',
  deleteErrRetry: '삭제에 실패했습니다. 잠시 후 다시 시도하세요.',

  triggerErrNotFound: '프로젝트가 존재하지 않습니다. 새로고침한 후 다시 시도하세요.',
  triggerErrStatus: '트리거에 실패했습니다({status})',
  triggerErrRetry: '트리거에 실패했습니다. 잠시 후 다시 시도하세요.',
}
