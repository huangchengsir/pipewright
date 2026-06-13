export default {
  title: 'Serverstatus',
  subtitle: 'CPU-Last, Speicher- und Festplattennutzung aller registrierten Server, live über SSH erfasst',
  reachableSummary: '{reachable}/{total} erreichbar',
  autoRefresh: 'Aktualisiert automatisch alle {n} s',
  loadingAria: 'Serverstatus wird geladen',
  errTitle: 'Laden des Serverstatus fehlgeschlagen',
  errConnect: 'Verbindung zum Server nicht möglich. Prüfe, ob das Backend läuft, und versuche es erneut.',
  errLoadStatus: 'Laden des Serverstatus fehlgeschlagen ({status})',
  errLoadRetry: 'Laden des Serverstatus fehlgeschlagen. Bitte versuche es später erneut.',
  emptyTitle: 'Noch keine registrierten Server',
  emptyDesc: 'Registriere Zielserver unter „Einstellungen › Server“, dann werden hier ihre Ressourcenmetriken angezeigt.',
}
