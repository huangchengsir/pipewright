export default {
  title: 'Container',
  subtitle: 'Container und Images pro Host über alle registrierten Server hinweg verwalten',
  countSummary: '· Insgesamt {total} Container · {running} laufen',
  autoRefresh: '· Aktualisiert sich automatisch alle {n} s',

  aiAssistant: '✦ KI-Assistent',
  prune: '🧹 Aufräumen',
  bulkEnter: 'Stapel',
  bulkExit: 'Stapel beenden',
  create: '+ Neuer Container',

  loadingAria: 'Containerliste wird geladen',
  errTitle: 'Containerliste konnte nicht geladen werden',
  errConnect: 'Verbindung zum Server nicht möglich. Prüfen Sie, ob das Backend läuft, und versuchen Sie es erneut.',
  errLoadStatus: 'Containerliste konnte nicht geladen werden ({status})',
  errLoadRetry: 'Containerliste konnte nicht geladen werden. Bitte versuchen Sie es später erneut.',

  emptyTitle: 'Noch keine registrierten Server',
  emptyDesc: 'Registrieren Sie Zielserver unter „Einstellungen › Server“, dann werden deren Container und Images hier zusammengefasst.',

  kpiTotal: 'Container gesamt',
  kpiRunning: 'Laufend',
  kpiStopped: 'Gestoppt',
  kpiHosts: 'Server mit Containern',
  kpiStripAria: 'Aggregierte Container-Statistik',

  filterAria: 'Container nach Status filtern',
  filterAll: 'Alle',
  filterRunning: 'Laufend',
  filterStopped: 'Gestoppt',
  filterPaused: 'Pausiert',

  searchPlaceholder: 'Container nach Name / Image suchen',
  searchAria: 'Container nach Name oder Image suchen',
  searchClear: 'Suche löschen',

  bulkAria: 'Stapelaktionen',
  bulkSelected: 'Ausgewählt',
  bulkSelectedUnit: '',
  bulkClear: 'Auswahl löschen',
  actionStart: 'Starten',
  actionStop: 'Stoppen',
  actionRestart: 'Neu starten',
  actionDelete: 'Löschen',

  confirmTitle: '{n} Container im Stapel {label}?',
  confirmBodyRm: 'Die ausgewählten Container werden gelöscht (docker rm). Laufende Container müssen zuerst gestoppt werden, sonst schlägt das Löschen fehl (zählt als Fehler).',
  confirmBodyAction: 'Die Aktion „{label}“ wird auf die {n} ausgewählten Container angewendet; zugehörige Dienste können kurz unterbrochen werden.',
  confirmLabel: '{n} {label}',

  toastDone: 'Stapel-{label} abgeschlossen',
  toastDoneDetail: '{n} erfolgreich',
  toastFail: 'Stapel-{label} fehlgeschlagen',
  toastFailDetail: '{n} fehlgeschlagen',
  toastPartial: 'Stapel-{label} teilweise abgeschlossen',
  toastPartialDetail: '{ok} erfolgreich · {fail} fehlgeschlagen',

  cardsAria: 'Container-Karten pro Server',
  aiContextContainer: '(docker-Host)',
}
