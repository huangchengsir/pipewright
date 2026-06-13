export default {
  title: 'OAuth 앱',
  sectionDesc:
    '각 git 플랫폼에 OAuth 앱(Client ID / Secret)을 등록하면 사용자는 자격 증명 보관소에서 원클릭으로 계정을 "연결"하여 PAT를 수동으로 붙여넣지 않고도 토큰을 자동으로 발급받을 수 있습니다. Secret은 저장 시 마스킹되며 작성 후에는 다시 읽을 수 없습니다.',
  retry: '다시 시도',

  providerCustomLabel: '자체 호스팅',
  providerGiteeDesc: 'Gitee',
  providerCustomDesc: '자체 호스팅 GitLab / Gitea 등',

  statusEnabled: '활성화됨',
  statusConfiguredIdle: '구성됨 · 비활성',
  statusUnconfigured: '구성 안 됨',

  clientIdPlaceholder: 'git 플랫폼의 OAuth 앱 페이지에서 가져오기',
  secretOptional: '(작성 후에는 마스크만 표시)',
  secretPlaceholderKeep: '비워 두면 기존 Secret을 유지합니다',
  secretPlaceholderNew: 'Client Secret 붙여넣기…',
  secretStored: '저장됨: {masked}',

  toggleAria: '{provider} OAuth 활성화',
  toggleTitle: '연결 활성화',
  toggleDesc: '활성화하면 이 플랫폼의 "연결" 버튼이 자격 증명 보관소에 표시됩니다',

  lastSaved: '마지막 저장 {time}',
  saveBtn: '저장',

  errNoServer: '서버에 연결할 수 없습니다. 백엔드 실행 여부를 확인한 후 다시 시도하세요',
  errVaultUnconfigured: '보관소에 master key가 구성되지 않았습니다. PIPEWRIGHT_MASTER_KEY 환경 변수를 설정하세요',
  errLoadStatus: '불러오기 실패({status})',
  errLoadGeneric: 'OAuth 앱을 불러오지 못했습니다. 잠시 후 다시 시도하세요',

  errClientIdRequiredEnabled: '활성화 시 Client ID는 필수입니다',
  errSecretRequiredEnabled: '활성화 시 Client Secret이 필요합니다',
  errBaseUrlRequiredCustom: '자체 호스팅 인스턴스에는 Base URL이 필요합니다',
  errClientIdRequired: 'Client ID를 입력하세요',
  errBaseUrlRequired: 'Base URL을 입력하세요',

  toastSaveFailed: '저장 실패',
  toastSaved: 'OAuth 앱이 저장되었습니다',
  errNoServerShort: '서버에 연결할 수 없습니다',
  unknownError: '알 수 없는 오류',

  justNow: '방금 전',
  minutesAgo: '{n}분 전',
  hoursAgo: '{n}시간 전',
  daysAgo: '{n}일 전',
}
