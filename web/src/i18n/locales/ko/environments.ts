export default {
  title: '환경',
  subtitle: '환경별로 집계된 배포 이력과 현재 활성 버전 · 마지막 성공 배포로 원클릭 롤백',
  project: '프로젝트',
  filterByProject: '프로젝트로 필터',
  selectProject: '프로젝트를 선택하세요',

  emptySelectTitle: '프로젝트를 선택하세요',
  emptySelectDesc: '배포 이력은 프로젝트별로 집계됩니다. 먼저 위에서 프로젝트를 선택하세요.',
  emptyTitle: '이 프로젝트에는 아직 배포 이력이 없습니다',
  emptyDesc:
    '환경에 배포가 실행되면(webhook 브랜치 매핑으로 환경 이름이 해석되고 배포가 완료되면) 여기에 환경별 타임라인이 표시됩니다.',

  errLoadTitle: '배포 이력 불러오기 실패',
  errNetwork: '서버에 연결할 수 없습니다. 백엔드가 실행 중인지 확인한 후 다시 시도하세요.',
  errLoad: '배포 이력 불러오기 실패({status})',
  errLoadRetry: '배포 이력 불러오기에 실패했습니다. 잠시 후 다시 시도하세요.',

  active: '활성',
  activeVersionTitle: '현재 활성 버전',
  noActiveVersion: '활성 버전 없음',
  noFullSuccessTitle: '아직 전부 성공한 배포가 없습니다',
  targetCount: '대상 호스트 {n}대',

  rollback: '롤백',
  rollbackEnabledTitle: '마지막 성공 배포로 롤백',
  rollbackDisabledTitle: '롤백할 이전 성공 배포가 없습니다',
  rollbackTitle: '환경 「{env}」 롤백',
  rollbackBody:
    '이 환경을 마지막 성공 배포(실행 {commit} · {when})로 롤백하여 해당 산출물을 원래 대상 호스트에 다시 배포합니다. 이 작업은 실제 배포를 트리거합니다.',
  rollbackConfirm: '롤백 확인',
  rollbackFailedStatus: '롤백 실패({status})',
  rollbackFailedRetry: '롤백에 실패했습니다. 잠시 후 다시 시도하세요.',

  toastRolledBack: '환경 「{env}」을(를) 롤백했습니다',
  toastRolledBackDetail: '산출물을 대상 호스트 {n}대에 다시 배포했습니다',
  toastRollbackPartial: '환경 「{env}」 롤백이 일부 실패했습니다',
  toastRollbackPartialDetail: '대상 호스트 {failed}/{total}대 실패',
  toastRollbackFailed: '환경 「{env}」 롤백에 실패했습니다',

  timelineAria: '{env} 배포 이력',
}
