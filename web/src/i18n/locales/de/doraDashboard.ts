export default {
  title: 'DORA-Metriken',
  subtitle:
    'Aus vorhandenen Ausführungsdaten aggregierte Sicht auf die Lieferleistung · Deployment-Häufigkeit / Vorlaufzeit / Änderungsfehlerrate / Wiederherstellungszeit',
  generatedAt: '· Daten Stand {time}',

  window7d: 'Letzte 7 Tage',
  window30d: 'Letzte 30 Tage',
  window90d: 'Letzte 90 Tage',

  projectLabel: 'Projekt',
  projectFilterAria: 'Nach Projekt filtern',
  allProjects: 'Alle Projekte',
  windowAria: 'Zeitfenster',

  errTitle: 'DORA-Metriken konnten nicht geladen werden',
  errOffline: 'Server nicht erreichbar. Prüfe, ob das Backend läuft, und versuche es erneut',
  errLoadStatus: 'DORA-Metriken konnten nicht geladen werden ({status})',
  errLoadRetry: 'DORA-Metriken konnten nicht geladen werden. Bitte später erneut versuchen',

  summaryDeployments: 'Deployments in {days} Tagen',
  summarySuccess: 'Erfolgreich',
  summaryFailed: 'Fehlgeschlagen',

  metricDeployFreq: 'Deployment-Häufigkeit',
  metricLeadTime: 'Vorlaufzeit',
  metricCfr: 'Änderungsfehlerrate',
  metricMttr: 'Mittlere Wiederherstellungszeit',

  capDeployFreq: '{count} erfolgreiche Deployments in {days} Tagen',
  capLeadTime: 'Median der Zeit Commit→Produktion über {count} erfolgreiche Deployments',
  capLeadTimeEmpty: 'Noch keine erfolgreichen Deployments zur Berechnung der Vorlaufzeit',
  capCfr: '{failed} / {total} Deployments fehlgeschlagen',
  capMttr: 'Median der Dauer über {count} „Fehler→Wiederherstellung“-Paare',
  capMttrEmpty: 'Keine „Fehler→Wiederherstellung“-Paare in diesem Zeitfenster',

  noteLead:
    'Methodik: ein „Deployment“ = eine Ausführung, die einen Endzustand erreicht; fehlt der Commit-Zeitpunkt, wird die Vorlaufzeit durch den Zeitpunkt der Einreihung angenähert. Aus CI-Ausführungsdaten abgeleitete DORA-Metriken sind eine ',
  noteEmphasis: 'Näherung',
  noteTrail: ' und dienen nur als Referenz, nicht als SLA-Grundlage.',
}
