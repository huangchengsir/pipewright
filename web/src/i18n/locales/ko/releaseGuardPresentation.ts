export default {
  aiTitle: 'AI 해석',
  boundaryDetails: '보고서 범위 및 증거 보존',
  titles: {
    notFound: '샘플 시점에 컨테이너 없음 확인됨',
    unknown: '이 증거는 아직 해석할 수 없습니다',
    unavailable: '유효한 런타임 샘플 없음',
    running: '샘플 시점에 컨테이너 실행 중',
    exited: '샘플 시점에 컨테이너 중지됨',
    oom: '메모리 부족 종료 기록됨',
    image: '예상 이미지 식별 정보 기록됨',
    gateFailed: '배포 시 상태 검사 실패',
    logs: '제한된 관찰 기간 로그 보존됨',
    unhealthy: '샘플 시점에 Docker 상태 검사 실패',
    logsUnavailable: '관찰 기간 로그를 가져오지 못함',
    logsPartial: '관찰 기간 로그 일부만 보존됨',
    logsEmpty: '로그 기간 수집됨, 반환된 행 없음',
  },
  logExcerpt: '로그 발췌(최대 600자, 전체 내용은 기술 세부 정보 참조)',
  snapshotScope:
    '매번 선택하고 승인한 스냅샷만 해석합니다. 전체 보고서를 분석하거나 자동 조사하지 않습니다.',
  nextTitle: '다음 단계',
  nextDescription: '실패 증거를 확인한 후 변경 조사 또는 재관찰을 결정하세요.',
  investigateAction: '관련 변경 조사',
  why: '검증 실패 이유',
  failedFallback:
    '규칙이 실패를 확인했습니다. 세부 조건은 확인할 수 없습니다. 기술 정보를 확인하세요.',
  unknownTitle: '아직 확인되지 않음',
  unknownScope:
    '샘플만으로 지속 가용성이나 서비스 정상 여부를 입증할 수 없습니다. 근본 원인은 별도 증거가 필요합니다.',
  unconfirmed: '확인되지 않은 정보입니다. 원본은 기술 세부 정보에 보존됩니다.',
  unknown: '알 수 없음',
  unknownTarget: '확인되지 않은 대상',
  unknownCondition: '알 수 없는 검사 조건',
  reportMissing: '보고서를 찾을 수 없습니다.',
  technicalDetails: '기술 세부 정보',
  reportDetails: '보고서 원본 데이터',
  evidenceReference: '증거 {n} 보기',
  evidenceLoaded: '{n}개 로드됨. 현재 로드된 증거만 포함합니다.',
  continueFinding: '이 증거 계속 찾기',
  find: {
    found: '인용된 증거를 찾았습니다.',
    pending:
      '증거가 아직 로드되지 않았습니다. 페이지별로 계속 찾을 수 있습니다.',
    loading: '증거를 읽고 찾는 중…',
    missing: '마지막 페이지까지 확인했지만 증거를 찾지 못했습니다.',
    expired: '증거가 만료되었습니다. 과거 결론은 보존됩니다.',
    error: '읽기에 실패했습니다. 수동으로 계속 찾을 수 있습니다.',
    stalled:
      '커서가 진행되지 않아 검색을 중지했습니다. 보고서를 새로 고치세요.',
  },
  kinds: {
    runtime: '런타임 샘플',
    baseline: '배포 전 기준',
    expected_image: '예상 이미지 기록',
    deployment_health_gate: '배포 시 상태 검사',
    logs: '관찰 기간 로그',
    unknown: '알 수 없는 증거 유형',
  },
  running: '실행 상태',
  runningValue: '실행 중',
  stoppedValue: '중지됨',
  exitCode: '종료 코드',
  oom: '메모리 부족 종료',
  restarts: '재시작 횟수',
  yes: '있음',
  no: '없음',
  health: 'Docker 상태',
  healthValues: {
    healthy: 'Docker 검사 통과',
    unhealthy: 'Docker 검사 실패',
    starting: 'Docker 검사 시작 중',
    absent: '미설정, 서비스 정상 여부 알 수 없음',
    unknown: '알 수 없음',
  },
  imageIdentity: '이미지 식별 정보',
  imageRecorded: '식별 값',
  recorded: '기록됨, 기술 세부 정보 참조',
  identityValues: {
    config_id: '로컬 Docker Image ID',
    manifest: '매니페스트 다이제스트',
    index: '인덱스 다이제스트',
    repository_digest_unknown: '다이제스트 유형 알 수 없음',
    unknown: '알 수 없음',
  },
  gate: '배포 검사',
  failed: '실패',
  windowStart: '로그 기간 시작',
  windowEnd: '로그 기간 종료',
  logGap: '로그 수집 일정 공백',
  fileStatus: {
    added: '추가됨',
    modified: '수정됨',
    deleted: '삭제됨',
    renamed: '이름 변경됨',
    copied: '복사됨',
    unknown: '상태 알 수 없음',
  },
  timeline: {
    deploymentStarted: '배포 시도 시작됨',
    activationStarted: '컨테이너 활성화 시작됨',
    deploymentFinished: '배포 시도 종료됨',
    baselineFailing: '배포 전 기준에 이미 런타임 실패 기록됨',
    logsRequested: '제한된 로그 수집 요청됨',
    logsCollected: '관찰 기간 로그 수집 결과 저장됨',
    logsUnavailable: '관찰 기간 로그를 가져올 수 없음',
    finished: '관찰 작업 종료됨',
    investigated: '변경 관련 조사 업데이트됨',
    evidenceExpired: '원본 증거 만료됨',
  },
  runtimeImageTruncated:
    '이미지 메타데이터가 잘렸습니다. 수집된 컨테이너 실행 사실은 유지되지만 전체 이미지 식별 정보는 확인되지 않았습니다.',
  targetBindingChanged:
    '실행 사실 수집 후 대상 설정이 변경되었습니다. 이 정보로 현재 대상 연결이 유효한지 확인할 수 없습니다.',
  targetBindingUnknown:
    '대상 변경 표시 형식이 잘못되었습니다. 현재 대상 연결은 확인되지 않았습니다.',
  logsPartialScope:
    '보존된 발췌만 볼 수 있습니다. 요청 기간에 생략되거나 잘린 내용이 있을 수 있습니다.',
  logLines: '수집된 로그 행 수',
  reasons: {
    sourceCacheBusy:
      '로컬 소스 캐시가 사용 중이어서 소스 비교를 가져오지 못했습니다.',
    sourceCacheUnreadable:
      '로컬 소스 캐시를 읽을 수 없어 소스 비교가 확인되지 않았습니다.',
    baseCommitNotCached:
      '기준 커밋이 로컬에 캐시되지 않아 정확한 소스 비교를 할 수 없습니다.',
    currentCommitNotCached:
      '현재 커밋이 로컬에 캐시되지 않아 정확한 소스 비교를 할 수 없습니다.',
    logTimeGap:
      '로그 수집에 시간 공백이 있습니다. 누락된 기간이 정상 동작을 입증하지는 않습니다.',
    serviceRestarted:
      '서비스 재시작으로 관찰이 중단되었습니다. 이것이 애플리케이션 실패를 입증하지는 않습니다.',
    resolvedSourceUnavailable:
      '검증 가능한 전체 소스 커밋이 없어 정확한 소스 비교를 할 수 없습니다.',
    sourceRunUnavailable:
      '해당 소스 실행 기록을 사용할 수 없거나 검증하지 못해 소스 커밋을 확인할 수 없습니다.',
    sourceRevisionConflict:
      '소스 커밋 기록이 충돌하여 정확한 소스 비교의 근거로 사용할 수 없습니다.',
    sourceRevisionNotConfirmed:
      '실행 기록과 배포 기록에서 소스 커밋이 일치하는지 확인되지 않아 소스 비교를 사용할 수 없습니다.',
    currentDeploymentIdentityUnavailable:
      '현재 배포 식별 기록을 사용할 수 없어 신뢰할 수 있는 배포 비교 기준을 검증할 수 없습니다.',
    deploymentHistoryUnavailable:
      '배포 이력 기록을 사용할 수 없어 변경 비교 기준을 검증할 수 없습니다.',
  },
  citationLabel: '{candidate} · {context} · 증거 {n}',
}
