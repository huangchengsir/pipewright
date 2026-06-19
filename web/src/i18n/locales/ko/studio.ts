export default {
  // ─── header ────────────────────────────────────────────────────
  brandTitle: 'Pipewright · 라이브러리',
  brandSubtitle: '커스텀 노드 스튜디오 (Custom Node Studio)',
  namePlaceholder: '노드 이름 지정…',
  nameAria: '노드 이름',
  cancel: '취소',
  saving: '저장 중…',
  saveToLibrary: '라이브러리에 저장',

  // ─── hero ──────────────────────────────────────────────────────
  heroEyebrow: '로우코드 · 한 번 정의하고 어디서나 재사용',
  heroTitlePre: '매개변수화 가능한 단계들을 ',
  heroTitleEm: '드래그',
  heroTitlePost: '하여 재사용 가능한 노드로 구성',
  heroDescPre: '왼쪽 그룹화된 팔레트에서 블록을 가운데 캔버스로 드래그하고 카드를 재정렬하세요. 오른쪽에서는 변수를 노드 표면 매개변수로 「승격」합니다. 하단은 기존',
  heroDescPost: '노드로 실시간 컴파일되며, 백엔드 변경은 전혀 없고 인스턴스는 승격된 몇 항목만 설정합니다.',

  // ─── banners ───────────────────────────────────────────────────
  loadingBanner: '불러오는 중…',
  loadFailed: '불러오기 실패',
  loadFailedCode: '불러오기 실패({code})',
  saveFailed: '저장 실패',
  saveFailedCode: '저장 실패({code})',
  errNameRequired: '노드 이름은 비워둘 수 없습니다',
  errNeedCommandStep: '명령을 생성하는 단계가 최소 하나는 있어야 합니다',
  updatedToast: '커스텀 노드 「{name}」을(를) 업데이트했습니다',
  createdToast: '커스텀 노드 「{name}」을(를) 생성했습니다',

  // ─── palette ───────────────────────────────────────────────────
  paletteHintPre: '아래 블록을 가운데 캔버스로 ',
  paletteHintStrong: '드래그',
  paletteHintPost: '하세요(또는 클릭하여 끝에 추가).',

  // ─── compose canvas ────────────────────────────────────────────
  composeTitle: '단계 구성 · 카드를 드래그하여 재정렬',
  composeEmpty: '여기에 블록을 드래그하여 시작 →',
  moveUp: '위로 이동',
  moveDown: '아래로 이동',
  deleteStep: '삭제',

  // ─── step field labels ─────────────────────────────────────────
  fieldCommandMultiline: '명령(여러 줄 가능)',
  fieldInstallCommand: '설치 명령',
  fieldEchoText: '출력 텍스트',
  phEchoText: '빌드 시작…',
  fieldEnvKey: '변수 이름',
  fieldEnvValue: '값',
  fieldTargetDir: '대상 디렉터리',
  fieldPathDir: 'PATH에 추가할 디렉터리',
  fieldArtifactPath: '산출물 경로 (glob)',
  fieldSaveAs: '다른 이름으로 저장',
  fieldArchiveFile: '아카이브 파일',
  fieldExtractTo: '압축 해제 디렉터리',
  fieldCondition: 'shell 조건(거짓이면 이후 건너뜀, set -e 안전)',
  fieldCommand: '명령',
  fieldRetryCount: '횟수',
  fieldDelaySecs: '간격(초)',
  fieldTimeoutSecs: '제한 시간(초)',
  fieldSleepSecs: '대기 초',
  fieldProbeUrl: '프로브 URL',
  fieldNote: '메모(# 주석으로 컴파일되며 실행되지 않음)',
  fieldTestCommand: '테스트 명령',
  fieldReportPath: '리포트 경로 (JUnit)',
  fieldMinCoverage: '커버리지 게이트 %',

  // ─── right rail tabs ───────────────────────────────────────────
  tabParams: '승격 매개변수',
  tabMeta: '노드 표면',

  // ─── params tab ────────────────────────────────────────────────
  paramsHintPre: '단계에서',
  paramsHintPost: '(으)로 참조합니다. 인스턴스 재사용 시 이 항목만 설정합니다.',
  paramsEmpty: '아직 승격된 매개변수가 없습니다. 전체 스크립트를 하드코딩해도 되지만, 승격해야 인스턴스에서 변경할 수 있습니다.',
  removeParamAria: '매개변수 제거',
  newParamLabel: '새 매개변수',
  phDisplayLabel: '표시 라벨',
  phDefaultValue: '기본값',
  phOptions: '쉼표로 구분된 옵션, 예: 20, 18, 22',
  addParam: '＋ 매개변수 승격',

  // ─── param type options ────────────────────────────────────────
  paramTypeText: '텍스트',
  paramTypeSelect: '열거',
  paramTypeNumber: '숫자',
  paramTypeToggle: '불리언',

  // ─── meta tab ──────────────────────────────────────────────────
  metaImagePre: '실행 이미지(',
  metaImagePost: ' 포함 가능)',
  metaIcon: '아이콘',
  metaCategory: '분류',
  phCategory: '빌드 및 산출물',
  metaSummaryPre: '한 줄 설명(',
  metaSummaryPost: ' 포함 가능)',
  imgPlaceholder: '예: node:20-alpine',
  summaryPlaceholder: '예: npm으로 빌드하고 dist 생성',
  metaHint: '분류는 「노드 추가」 선택기의 어느 그룹에 표시될지 결정합니다. 설명과 아이콘은 재사용자가 처음 보는 카드입니다.',
  defaultCategory: '커스텀',

  // ─── bottom: compiled output ───────────────────────────────────
  compiledTitle: '컴파일 산출물 · templated 노드 config(백엔드가 그대로 실행)',
  undeclaredWarn: '⚠ 단계가 승격되지 않은 매개변수를 참조합니다: {refs}(그대로 유지되며 인스턴스에서 변경 불가)',
  compiledComment: '# templated 커스텀 노드 config —— 백엔드가 renderTemplate({open}) 후 컨테이너 내에서 실행',
  compiledEmpty: '(아직 단계가 없습니다)',

  // ─── bottom: instance preview ──────────────────────────────────
  previewTitle: '인스턴스 미리보기 · 파이프라인에 드래그한 후 보이는 것은 이것뿐',
  unnamedNode: '이름 없는 노드',
  customLabel: '커스텀',
  previewNote: '이것이 바로 n8n의 「매개변수 승격→인스턴스 짧은 목록」 / Node-RED Subflow properties 패러다임입니다. 재사용자는 내부 스크립트를 이해할 필요 없이 노출된 매개변수만 설정합니다.',
}
