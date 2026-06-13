export default {
  title: 'Umgebungen',
  subtitle:
    'Nach Umgebung gruppierter Deployment-Verlauf und die aktuell aktive Version · mit einem Klick auf das letzte erfolgreiche Deployment zurückrollen',
  project: 'Projekt',
  filterByProject: 'Nach Projekt filtern',
  selectProject: 'Projekt auswählen',

  emptySelectTitle: 'Projekt auswählen',
  emptySelectDesc: 'Der Deployment-Verlauf wird nach Projekt gruppiert – wähle zuerst oben eines aus.',
  emptyTitle: 'Für dieses Projekt gibt es noch keinen Deployment-Verlauf',
  emptyDesc:
    'Sobald ein Deployment für eine Umgebung ausgeführt wurde (das Branch-Mapping des Webhooks löst den Umgebungsnamen auf und das Deployment ist abgeschlossen), erscheint hier die nach Umgebung gruppierte Zeitleiste.',

  errLoadTitle: 'Laden des Deployment-Verlaufs fehlgeschlagen',
  errNetwork: 'Server nicht erreichbar. Prüfe, ob das Backend läuft, und versuche es erneut.',
  errLoad: 'Laden des Deployment-Verlaufs fehlgeschlagen ({status})',
  errLoadRetry: 'Laden des Deployment-Verlaufs fehlgeschlagen. Bitte später erneut versuchen.',

  active: 'Aktiv',
  activeVersionTitle: 'Aktuell aktive Version',
  noActiveVersion: 'Keine aktive Version',
  noFullSuccessTitle: 'Noch kein vollständig erfolgreiches Deployment',
  targetCount: '{n} Zielhosts',

  rollback: 'Zurückrollen',
  rollbackEnabledTitle: 'Auf das letzte erfolgreiche Deployment zurückrollen',
  rollbackDisabledTitle: 'Kein vorheriges erfolgreiches Deployment zum Zurückrollen',
  rollbackTitle: 'Umgebung „{env}“ zurückrollen',
  rollbackBody:
    'Dies rollt die Umgebung auf das letzte erfolgreiche Deployment (Lauf {commit} · {when}) zurück und stellt diese Artefakte erneut auf den ursprünglichen Zielhosts bereit. Diese Aktion löst ein echtes Deployment aus.',
  rollbackConfirm: 'Zurückrollen bestätigen',
  rollbackFailedStatus: 'Zurückrollen fehlgeschlagen ({status})',
  rollbackFailedRetry: 'Zurückrollen fehlgeschlagen. Bitte später erneut versuchen.',

  toastRolledBack: 'Umgebung „{env}“ zurückgerollt',
  toastRolledBackDetail: 'Artefakte auf {n} Zielhosts erneut bereitgestellt',
  toastRollbackPartial: 'Zurückrollen der Umgebung „{env}“ teilweise fehlgeschlagen',
  toastRollbackPartialDetail: '{failed}/{total} Zielhosts fehlgeschlagen',
  toastRollbackFailed: 'Zurückrollen der Umgebung „{env}“ fehlgeschlagen',

  timelineAria: 'Deployment-Verlauf von {env}',
}
