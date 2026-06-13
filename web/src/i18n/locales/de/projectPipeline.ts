export default {
  // ─── Kopfleiste / Brotkrumen ────────────────────────────────────
  breadcrumbAria: 'Brotkrumen-Navigation',
  breadcrumbProjects: 'Projekte',
  title: 'Pipeline-Konfiguration',

  // ─── Tabs ───────────────────────────────────────────────────────
  tabCanvas: 'Pipeline-Leinwand',
  tabVars: 'Variablen & Cache',
  tabTriggers: 'Auslöser-Einstellungen',
  tabEnvs: 'Umgebungen & Anmeldedaten',
  tabStripAria: 'Tabs der Pipeline-Konfiguration',

  // ─── Pipeline as Code (GitOps · FR-8-12) ────────────────────────
  pacTitle: 'Pipeline as Code',
  pacOnHint: 'Läufe lesen .pipewright.yml aus dem Lauf-Branch im Repository (Rückfall auf diese Konfiguration, wenn die Datei fehlt oder ungültig ist).',
  pacOffHint: 'Wenn aktiviert, liest jeder Lauf .pipewright.yml aus dem Repository-Stamm im Lauf-Branch (Rückfall auf diese Konfiguration, wenn fehlend oder ungültig).',
  pacToggleFailed: 'Umschalten fehlgeschlagen. Bitte erneut versuchen.',

  // ─── PR-Statusprüfungen (Commit-Status-Rückschreibung · Story 8-9 / FR-8-9) ─
  prStatusTitle: 'PR-Statusprüfungen',
  prStatusOnHint: 'Wenn ein Lauf endet, erkennt Pipewright die Repository-Plattform (GitHub/Gitee) und schreibt den Erfolgs-/Fehlerstatus des Commits mit den Projektanmeldedaten als PR-Prüfung zurück (nach bestem Bemühen; Fehler beeinflussen den Lauf nie).',
  prStatusOffHint: 'Wenn aktiviert, erkennt das Ende eines Laufs die Repository-Plattform (GitHub/Gitee) und schreibt den Commit-Status mit den Projektanmeldedaten als PR-Prüfung zurück (nach bestem Bemühen).',
  prStatusToggleFailed: 'Umschalten fehlgeschlagen. Bitte erneut versuchen.',

  // ─── Repository-Konfiguration vorschauen (GitOps · .pipewright.yml an einer Ref abrufen & prüfen) ──
  pacPreviewBtn: 'Repo-Konfig vorschauen',
  pacPreviewTitle: 'Repository-Konfiguration vorschauen',
  pacPreviewSub: 'Datei des Repositorys an einem Branch/Tag/Commit abrufen und prüfen:',
  pacPreviewCloseAria: 'Vorschau schließen',
  pacPreviewCloseBtn: 'Schließen',
  pacPreviewRefLabel: 'Branch / Tag / Commit',
  pacPreviewFetch: 'Abrufen & prüfen',
  pacPreviewNotFound: 'Keine .pipewright.yml unter {ref} gefunden; Läufe greifen auf die hier konfigurierte Pipeline zurück.',
  pacPreviewInvalid: 'Die .pipewright.yml unter {ref} hat die Prüfung nicht bestanden; Läufe greifen stillschweigend auf die hier konfigurierte Pipeline zurück:',
  pacPreviewValid: 'Die .pipewright.yml unter {ref} ist gültig mit {count} Stufe(n); Läufe verwenden sie.',
  pacPreviewJobCount: '{count} Job(s)',
  pacPreviewConnFailed: 'Verbindung fehlgeschlagen. Netzwerk prüfen und erneut versuchen.',
  pacPreviewFailed: 'Vorschau fehlgeschlagen ({status}). Bitte erneut versuchen.',
  pacPreviewFailedRetry: 'Vorschau fehlgeschlagen. Bitte erneut versuchen.',

  // ─── Symbolleisten-Schaltflächen ────────────────────────────────
  aiGenerate: 'Pipeline per KI generieren',
  importYaml: 'Aus YAML importieren',
  templates: 'Vorlagen',
  validate: 'Konfiguration validieren',
  closeValidationPanel: 'Validierungsbereich schließen',
  badgeReady: 'Bereit',
  badgeErrors: '{n} Fehler',
  saving: 'Wird gespeichert…',
  saveDraft: 'Entwurf speichern',

  // ─── Banner / Status ────────────────────────────────────────────
  dismiss: 'Schließen',
  draftSaved: 'Pipeline-Entwurf gespeichert',
  retry: 'Erneut versuchen',
  loading: 'Wird geladen',

  // ─── Ladefehler ─────────────────────────────────────────────────
  errNoServer: 'Server nicht erreichbar. Prüfen Sie, ob das Backend läuft, und versuchen Sie es erneut.',
  errProjectNotFound: 'Projekt nicht gefunden. Bitte überprüfen Sie die Projekt-ID.',
  errLoadFailedStatus: 'Pipeline konnte nicht geladen werden ({status})',
  errLoadFailedRetry: 'Pipeline konnte nicht geladen werden. Bitte versuchen Sie es später erneut.',

  // ─── Speicherfehler ─────────────────────────────────────────────
  errSaveFailedRetry: 'Speichern fehlgeschlagen. Bitte versuchen Sie es später erneut.',
  errSaveFailedStatus: 'Speichern fehlgeschlagen ({status})',
  errInvalidStage: 'Der Phasenname darf nicht leer sein und kind muss ein zulässiger Wert sein. Bitte prüfen und erneut versuchen.',
  errInvalidJob: 'Job-Name oder -Typ dürfen nicht leer sein. Bitte ergänzen und erneut versuchen.',
  errDuplicateId: 'Doppelte Phasen- oder Job-ID. Bitte entfernen Sie Duplikate und versuchen Sie es erneut.',
  errInvalidBuild: 'Das Build-Modell muss dockerfile/toolchain sein und der Artefakttyp muss image/jar/dist sein.',
  errInvalidVar: 'Der Variablenschlüssel darf nicht leer sein und muss innerhalb seines Geltungsbereichs eindeutig sein; secret-Variablen erfordern eine Vault-Anmeldedaten.',
  errInvalidEnvironment: 'Der Umgebungsname darf nicht leer sein und der Image-Registry-Typ muss harbor/acr/dockerhub/custom sein.',
  errCredentialNotFound: 'Die referenzierte Vault-Anmeldedaten existiert nicht. Bitte erneut auswählen und versuchen.',
  errVaultUnconfigured: 'Im Vault ist kein Master Key konfiguriert, daher können secret-Anmeldedaten nicht referenziert werden.',
}
