export default {
  // ─── shared ──────────────────────────────────────────────────────────────
  loadingShort: '로딩 중…',

  // ─── AuditTimeline ───────────────────────────────────────────────────────
  audit: {
    title: '감사 로그',
    sub: '누가 · 언제 · 무엇을',
    treeAria: '감사 타임라인',
    emptyLabel: '아직 감사 기록이 없습니다',
    emptyHint: '자격 증명이나 프로젝트의 생성·수정·삭제, webhook 시크릿 재설정, 실행 수동 트리거 등 민감한 작업은 모두 여기에 기록됩니다. 기록은 변조할 수 없습니다.',
    loadMore: '감사 기록 더 불러오기 →',
    via: '웹 콘솔',
    actorYou: '나',
    verbCreate: '생성',
    verbUpdate: '수정',
    verbDelete: '삭제',
    verbReset: '재설정',
    verbAdd: '연결',
    verbTrigger: '수동 트리거',
    verbDefault: '작업',
    nounCredential: '자격 증명',
    nounWebhookSecret: 'webhook 서명 시크릿',
    nounProject: '프로젝트',
    nounRun: '실행',
    errConnect: '서버에 연결할 수 없습니다. 백엔드가 실행 중인지 확인한 후 다시 시도하세요',
    errLoad: '감사 로그 불러오기 실패({status})',
    errLoadRetry: '감사 로그 불러오기에 실패했습니다. 잠시 후 다시 시도하세요',
  },

  // ─── CodeTree / CodeTreeNode ─────────────────────────────────────────────
  tree: {
    aria: '코드 디렉터리 트리',
    fileAria: '저장소 파일 트리',
    title: '파일',
    refTitle: '현재 ref: {ref}',
    loadingDir: '디렉터리 로딩 중…',
    emptyRepo: '빈 저장소 / 소스를 읽을 수 없음',
    emptyDir: '빈 디렉터리',
    errConnect: '서버에 연결할 수 없습니다',
    errNotFound: '경로가 존재하지 않습니다',
    errLoad: '불러오기 실패({status})',
    errLoadGeneric: '불러오기 실패',
  },

  // ─── CodeViewer ──────────────────────────────────────────────────────────
  code: {
    viewAria: '코드 보기',
    editorAria: '코드 편집기(읽기 전용)',
    noFileSelected: '선택된 파일 없음',
    truncated: '잘림',
    truncatedTitle: '파일이 너무 커서 앞부분만 표시합니다',
    idleTitle: '왼쪽에서 파일을 선택하여 보기',
    idleSub: '저장소 소스를 읽기 전용으로 탐색하며 구문 강조를 표시합니다. 편집이나 커밋은 할 수 없습니다.',
    binaryTitle: '바이너리 파일은 미리 볼 수 없습니다',
    degradedTitle: '소스를 읽을 수 없음',
    degradedSub: '저장소 복제에 실패했거나 현재 환경에서 접근할 수 없습니다. 잠시 후 다시 시도하거나 프로젝트 저장소 설정을 확인하세요.',
    errTitle: '파일 불러오기 실패',
    fallbackRegionAria: '코드 내용(일반 텍스트 대체)',
    fallbackNote: '구문 강조 컴포넌트 로딩에 실패하여 일반 텍스트 보기로 전환했습니다.',
    errConnect: '서버에 연결할 수 없습니다',
    errNotFound: '파일이 존재하지 않습니다',
    errLoad: '불러오기 실패({status})',
    errLoadGeneric: '불러오기 실패',
  },

  // ─── ConfirmDialog ───────────────────────────────────────────────────────
  confirm: {
    cancel: '취소',
    confirm: '확인',
    typeLabelPrefix: '',
    typeLabelSuffix: '을(를) 입력하여 확인',
    typePlaceholder: '{text} 입력…',
    typeAria: '{text} 입력하여 작업 확인',
  },

  // ─── EmptyState ──────────────────────────────────────────────────────────
  empty: {
    defaultTitle: '데이터 없음',
  },

  // ─── ErrorState ──────────────────────────────────────────────────────────
  error: {
    defaultTitle: '불러오기 실패',
    retry: '다시 시도',
    aiUnavailableAria: 'AI 기능을 현재 사용할 수 없음',
    aiTitle: 'AI 실패 진단',
    aiTag: '사용 불가',
    aiDesc: 'LLM 공급자가 응답하지 않아 이번에는 진단이 생성되지 않았습니다. 실행 결과와 로그는 평소대로 기록되며 핵심 CI/CD에는 영향이 없습니다.',
    confidenceLabel: '신뢰도 {n}% · {level}',
    confidenceHigh: '높음',
    confidenceMedium: '중간',
    confidenceLow: '낮음',
  },

  // ─── ToastHost ───────────────────────────────────────────────────────────
  toast: {
    hostAria: '알림',
    itemAria: '{type} 알림: {title}',
    closeAria: '알림 닫기: {title}',
  },
}
