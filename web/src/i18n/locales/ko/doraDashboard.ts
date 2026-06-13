export default {
  title: 'DORA 지표',
  subtitle:
    '기존 실행 데이터를 집계한 배포 성과 뷰 · 배포 빈도 / 리드 타임 / 변경 실패율 / 복구 시간',
  generatedAt: '· 데이터 기준 {time}',

  window7d: '최근 7일',
  window30d: '최근 30일',
  window90d: '최근 90일',

  projectLabel: '프로젝트',
  projectFilterAria: '프로젝트로 필터링',
  allProjects: '모든 프로젝트',
  windowAria: '기간',

  errTitle: 'DORA 지표를 불러오지 못했습니다',
  errOffline: '서버에 연결할 수 없습니다. 백엔드가 실행 중인지 확인한 후 다시 시도하세요',
  errLoadStatus: 'DORA 지표를 불러오지 못했습니다({status})',
  errLoadRetry: 'DORA 지표를 불러오지 못했습니다. 잠시 후 다시 시도하세요',

  summaryDeployments: '{days}일간 배포',
  summarySuccess: '성공',
  summaryFailed: '실패',

  metricDeployFreq: '배포 빈도',
  metricLeadTime: '변경 리드 타임',
  metricCfr: '변경 실패율',
  metricMttr: '평균 복구 시간',

  capDeployFreq: '{days}일간 {count}회 성공 배포',
  capLeadTime: '{count}회 성공 배포의 커밋→배포 중앙값',
  capLeadTimeEmpty: '리드 타임을 집계할 성공 배포가 아직 없습니다',
  capCfr: '{failed} / {total}회 배포 실패',
  capMttr: '{count}개 「실패→복구」 구간의 중앙값',
  capMttrEmpty: '이 기간에는 「실패 후 복구」 쌍이 없습니다',

  noteLead:
    '집계 기준: 한 번의 「배포」 = 종료 상태에 도달한 하나의 실행. 커밋 시각이 없으면 리드 타임은 대기열 진입 시각으로 근사합니다. CI 실행 데이터 기반 DORA 지표는 ',
  noteEmphasis: '근사치',
  noteTrail: '로 참고용이며 SLA 근거로 사용하지 마세요.',
}
