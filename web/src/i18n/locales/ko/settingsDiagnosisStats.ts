export default {
  title: '진단 피드백 루프',
  subtitle:
    'AI 진단 품질 지표 —— 피드백이 많을수록 진단이 정확해지고, 잘못된 사례는 지식 베이스 시드가 됩니다',
  loading: '불러오는 중…',
  retry: '다시 시도',

  emptyTitle: '아직 진단 피드백이 없습니다',
  emptyHint: '실패한 실행의 AI 진단 패널에서 👍/👎를 누르면 통계가 여기에 집계됩니다',

  accuracy: '정확도',
  accuracyAria: '정확도 {pct}%',

  countTotal: '피드백 총수',
  countUp: '👍 유용함',
  countDown: '👎 개선 필요',

  trendTitle: '최근 추세',
  trendBarTitle: '{pct}% · {count}건',
  countUnit: '{count}건',

  correctionsTitle: '최근 수정(지식 베이스 시드)',
  correctionsEmpty: '👎에 첨부된 올바른 근본 원인이 아직 없습니다.',
  runLabel: '실행 {id}',

  errLoadFailed: '불러오기 실패({status})',
  errLoadGeneric: '진단 통계를 불러오지 못했습니다. 잠시 후 다시 시도하세요.',
}
