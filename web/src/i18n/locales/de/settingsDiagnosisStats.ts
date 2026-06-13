export default {
  title: 'Diagnose-Feedback-Schleife',
  subtitle:
    'Qualitätsmetriken der AI-Diagnose – je mehr Rückmeldungen, desto genauer die Diagnose, und fehlerhafte Fälle werden zu Saatgut der Wissensdatenbank',
  loading: 'Wird geladen…',
  retry: 'Erneut versuchen',

  emptyTitle: 'Noch keine Diagnose-Rückmeldungen',
  emptyHint: 'Tippe im AI-Diagnosepanel eines fehlgeschlagenen Laufs auf 👍/👎, dann werden die Statistiken hier zusammengeführt',

  accuracy: 'Genauigkeit',
  accuracyAria: 'Genauigkeit {pct}%',

  countTotal: 'Rückmeldungen gesamt',
  countUp: '👍 Hilfreich',
  countDown: '👎 Verbesserungsbedarf',

  trendTitle: 'Aktueller Trend',
  trendBarTitle: '{pct}% · {count} Einträge',
  countUnit: '{count} Einträge',

  correctionsTitle: 'Aktuelle Korrekturen (Saatgut der Wissensdatenbank)',
  correctionsEmpty: 'Noch keine korrekte Ursache an einem 👎 angehängt.',
  runLabel: 'Lauf {id}',

  errLoadFailed: 'Laden fehlgeschlagen ({status})',
  errLoadGeneric: 'Diagnosestatistiken konnten nicht geladen werden. Bitte später erneut versuchen.',
}
