export default {
  title: 'AI 제공자',
  subtitle:
    'Pipewright는 자체 모델을 학습하지 않습니다 —— 실패 진단과 설정 생성을 위해 직접 보유한 LLM을 연결하세요. 키는 이 인스턴스의 암호화된 보관소에만 저장되며 절대 외부로 유출되지 않습니다.',
  statusConfigured: '구성됨',
  statusUnconfigured: '미구성',
  retry: '다시 시도',

  providerClaudeTag: '진단 추천',
  providerOllamaDesc: '로컬 / 자체 호스팅',
  providerOllamaTag: '외부 전송 없음',

  guidanceAria: 'AI 구성 가이드',
  guidanceTitle: 'AI 진단을 활성화하려면 LLM을 구성하세요',
  guidanceBody:
    'Claude, OpenAI 또는 로컬 Ollama를 연결하면 파이프라인 실패 시 Pipewright가 근본 원인 가설과 수정 제안을 자동으로 생성하여 수동 로그 분석이 필요 없습니다.',

  selectProvider: '제공자 선택',
  providerRadioAria: 'AI 제공자 선택',
  selectProviderAria: '{name} 선택',
  providerConfig: '{name} 구성',
  lastSaved: '마지막 저장 {time}',

  apiKeyHint: '입력 후에는 마스킹된 값만 표시됩니다. 비워 두면 기존 키를 유지합니다',
  apiKeyReplacing: '교체 중…',
  apiKeyConfigured: '구성됨 ••••(비워 두면 변경 없음)',
  apiKeyPaste: 'API Key 붙여넣기…',
  apiKeyMaskedAria: '구성된 마스크: {masked}',

  ollamaHint: '로컬 Ollama는 API Key가 필요 없습니다 —— Ollama 서비스가 지정된 주소에서 실행 중인지 확인하세요.',

  baseUrlLabel: '접속 주소 (Base URL)',
  baseUrlHint: '기본값: {url}',

  modelLabel: '모델',
  modelHint: '진단에 사용하는 기본 모델, 예: claude-opus-4-7 / gpt-4o / llama3',

  testConnection: '연결 테스트',
  testOk: '연결 정상 · 지연 {ms}ms',
  testFail: '연결 실패',

  budgetLabel: '월 Token 한도',
  budgetHint: '초과 시 AI 진단을 일시 중지합니다(비워 두면 무제한. 이번 주기에는 선언만, 다음 Epic에서 강제 적용)',
  budgetPlaceholder: '예: 500000, 비워 두면 무제한',

  enableAi: 'AI 기능 활성화',
  enableAiDesc: '끄면 AI 진단이 조용히 건너뛰며 핵심 CI/CD 파이프라인에는 영향이 없습니다',

  dirtyNote: '저장하지 않은 변경 사항이 있습니다',
  cleanNote: '변경 사항 없음',
  discard: '취소',
  saveChanges: '변경 사항 저장',

  toastSaveSuccess: 'AI 설정이 저장되었습니다',
  toastSaveFailed: '저장 실패',

  errServerUnreachable: '서버에 연결할 수 없습니다. 백엔드가 실행 중인지 확인한 후 다시 시도하세요.',
  errServerUnreachableShort: '서버에 연결할 수 없습니다',
  errVaultUnconfigured: '보관소에 master key가 구성되지 않았습니다. PIPEWRIGHT_MASTER_KEY 환경 변수를 설정하세요.',
  errLoadFailed: '불러오기 실패({status})',
  errLoadGeneric: 'AI 설정을 불러오지 못했습니다. 잠시 후 다시 시도하세요.',
  errBudgetInvalid: '월 token 한도는 양의 정수이거나 비워 두어야 합니다',
  errProviderInvalid: '유효한 제공자를 선택하세요',
  errBaseUrlRequired: '접속 주소를 입력하세요',
  errApiKeyRequired: 'API Key는 비워 둘 수 없습니다(Ollama 외 필수)',
  errRequestFailed: '요청 실패({status})',
  errUnknown: '알 수 없는 오류',
}
