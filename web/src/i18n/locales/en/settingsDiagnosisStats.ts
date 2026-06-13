export default {
  title: 'Diagnosis Feedback Loop',
  subtitle:
    'AI diagnosis quality metrics — the more feedback, the more accurate the diagnosis, and wrong cases become knowledge-base seeds',
  loading: 'Loading…',
  retry: 'Retry',

  emptyTitle: 'No diagnosis feedback yet',
  emptyHint: 'Tap 👍/👎 in the AI diagnosis panel of a failed run, and stats will be aggregated here',

  accuracy: 'Accuracy',
  accuracyAria: 'Accuracy {pct}%',

  countTotal: 'Total feedback',
  countUp: '👍 Helpful',
  countDown: '👎 Needs work',

  trendTitle: 'Recent trend',
  trendBarTitle: '{pct}% · {count} items',
  countUnit: '{count} items',

  correctionsTitle: 'Recent corrections (knowledge-base seeds)',
  correctionsEmpty: 'No correct root cause attached to any 👎 yet.',
  runLabel: 'Run {id}',

  errLoadFailed: 'Load failed ({status})',
  errLoadGeneric: 'Failed to load diagnosis stats. Please try again later.',
}
