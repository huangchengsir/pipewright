export default {
  // ─── page header ───────────────────────────────────────────────
  title: 'Projekte',
  subtitle: 'Verwaltete Gitee-Repositorys – jedes Projekt entspricht einer Pipeline-Konfiguration und Bereitstellungszielen',
  newProject: 'Neues Projekt',
  retry: 'Erneut versuchen',

  // ─── toolbar / search / filter ─────────────────────────────────
  searchFilterAria: 'Projekte suchen und filtern',
  searchPlaceholder: 'Nach Name, Repository-URL oder Branch suchen…',
  searchAria: 'Projekte suchen',
  statusFilterAria: 'Nach Status filtern',
  statusAll: 'Alle Status',

  // ─── list states ───────────────────────────────────────────────
  loading: 'Wird geladen',
  emptyTitle: 'Noch keine Projekte',
  emptyHint: 'Verbinde dein erstes Gitee-Repository, konfiguriere dann eine Pipeline und stelle auf Zielservern bereit.',
  noMatchTitle: 'Keine passenden Projekte',
  noMatchHint: 'Passe den Suchbegriff oder den Statusfilter an und versuche es erneut.',
  clearFilter: 'Filter zurücksetzen',
  resultCount: '{n} Projekte',
  resultCountTotal: '(von {total})',

  // ─── project card ──────────────────────────────────────────────
  runStatusAria: 'Ausführungsstatus: {status}',
  lastRun: 'Letzte Ausführung',
  noRun: 'Noch keine Ausführung',
  targetServers: 'Zielserver',
  notBound: 'Nicht gebunden',
  credential: 'Repository-Anmeldedaten',
  credentialRefTitle: 'Anmeldedaten-Referenz (kein Klartext)',
  updatedAt: 'Aktualisiert {time}',

  // ─── card actions ──────────────────────────────────────────────
  actionRunTitle: 'Ausführung manuell auslösen · {name}',
  actionRunAria: 'Pipeline-Ausführung für Projekt {name} manuell auslösen',
  actionRenameTitle: '{name} umbenennen',
  actionRenameAria: 'Projekt {name} umbenennen',
  actionCodeTitle: 'Code durchsuchen · {name}',
  actionCodeAria: 'Code des Projekts {name} durchsuchen',
  actionPipelineTitle: 'Pipeline-Konfiguration · {name}',
  actionPipelineAria: 'Pipeline des Projekts {name} konfigurieren',
  actionDeleteTitle: '{name} löschen',
  actionDeleteAria: 'Projekt {name} löschen',

  // ─── shared modal ──────────────────────────────────────────────
  closeDialog: 'Dialog schließen',
  cancel: 'Abbrechen',
  save: 'Speichern',
  saving: 'Wird gespeichert…',

  // ─── trigger modal ─────────────────────────────────────────────
  triggerDialogAria: 'Manueller Auslöser · {name}',
  triggerTitle: 'Ausführung manuell auslösen',
  triggerSub: '{name} · wähle einen Branch und erstelle sofort eine Pipeline-Ausführung',
  branch: 'Branch',
  branchHint: '(optional, leer lassen, um den Standard-Branch des Projekts zu verwenden)',
  commit: 'Commit',
  commitHint: '(optional, leer lassen, um den HEAD des Branches zu verwenden)',
  commitPlaceholder: 'z. B. a3f1c2d',
  params: 'Parameter',
  paramsHintTyped: '(gemäß Definition ausfüllen; werden als Umgebungsvariablen in die Pipeline injiziert)',
  paramsHintFree: '(optional, werden als Umgebungsvariablen in die Pipeline injiziert)',
  triggering: 'Wird ausgelöst…',
  runNow: 'Jetzt ausführen',

  // ─── create modal ──────────────────────────────────────────────
  createSub: 'Verbinde ein Gitee-Repository und binde Repository-Anmeldedaten ein',
  fieldName: 'Projektname',
  fieldNamePlaceholder: 'z. B. acme-web',
  fieldRepo: 'Repository-URL',
  fieldCredHint: '(es werden nur Git-Token-Anmeldedaten angezeigt, niemals Klartext)',
  credLoading: 'Anmeldedaten werden geladen…',
  credSelect: 'Git-Token-Anmeldedaten auswählen',
  credEmptyPre: 'Noch keine Git-Token-Anmeldedaten. Gehe zuerst zum',
  credVaultLink: 'Anmeldedaten-Tresor',
  credEmptyPost: ', um welche hinzuzufügen.',
  fieldDefaultBranch: 'Standard-Branch',
  fieldDefaultBranchHint: '(optional, leer lassen zur automatischen Erkennung über den Verbindungstest)',
  testConnection: 'Verbindung testen',
  testing: 'Wird getestet…',
  testOk: 'Verbindung erfolgreich',
  testDetectedBranch: '· Standard-Branch {branch}',
  creating: 'Wird erstellt…',
  createSubmit: 'Projekt erstellen',

  // ─── rename modal ──────────────────────────────────────────────
  renameTitle: 'Projekt umbenennen',
  renameSub: 'Den Anzeigenamen des Projekts ändern',

  // ─── delete modal ──────────────────────────────────────────────
  deleteDialogAria: 'Löschen des Projekts bestätigen',
  deleteTitle: 'Projekt löschen',
  deleteSub: 'Diese Aktion kann nicht rückgängig gemacht werden',
  deleteConfirmPre: 'Möchtest du das Projekt',
  deleteConfirmPost: 'wirklich dauerhaft löschen? Seine Pipeline-Konfiguration, sein Ausführungsverlauf und die Anmeldedaten-Referenzen werden alle bereinigt.',
  deleting: 'Wird gelöscht…',
  confirmDelete: 'Löschen bestätigen',

  // ─── error messages ────────────────────────────────────────────
  errNetwork: 'Der Server ist nicht erreichbar. Prüfe, ob das Backend läuft, und versuche es erneut.',
  errNetworkRetry: 'Der Server ist nicht erreichbar. Bitte versuche es später erneut.',
  errLoadNetwork: 'Der Server ist nicht erreichbar. Prüfe, ob das Backend läuft, und versuche es erneut',
  errLoadStatus: 'Laden der Projekte fehlgeschlagen ({status})',
  errLoadRetry: 'Laden der Projekte fehlgeschlagen, bitte später erneut versuchen',
  errNameRequired: 'Bitte gib einen Projektnamen ein',
  errRepoRequired: 'Bitte gib eine Repository-URL ein',
  errRepoFormat: 'Ungültiges Repository-URL-Format. Sie muss mit https:// oder git{\'@\'} beginnen',
  errCredRequired: 'Bitte wähle Repository-Anmeldedaten aus',
  errRepoFirst: 'Bitte gib zuerst eine Repository-URL ein',
  errCredFirst: 'Bitte wähle zuerst Repository-Anmeldedaten aus',
  errNameEmpty: 'Der Projektname darf nicht leer sein',

  testErrCredential: 'Anmeldedaten-Fehler: Prüfe, ob dein Gitee-Zugriffstoken gültig ist, und aktualisiere es im Anmeldedaten-Tresor.',
  testErrUnreachable: 'Repository nicht erreichbar: Stelle sicher, dass die URL korrekt ist und das Repository existiert und zugänglich ist.',
  testErrVault: 'Im Tresor ist kein Master Key konfiguriert, daher können die Anmeldedaten nicht gelesen werden.',
  testErrStatus: 'Verbindungstest fehlgeschlagen ({status})',
  testErrRetry: 'Verbindungstest fehlgeschlagen, bitte später erneut versuchen.',

  createErrCredField: 'Anmeldedaten-Fehler: Prüfe, ob das Zugriffstoken gültig ist',
  createErrCredBanner: 'Validierung der Anmeldedaten fehlgeschlagen. Wechsle die Anmeldedaten oder aktualisiere sie im Anmeldedaten-Tresor.',
  createErrRepoField: 'Repository nicht erreichbar: Stelle sicher, dass die URL korrekt und zugänglich ist',
  createErrRepoBanner: 'Repository-URL nicht erreichbar, Erstellung fehlgeschlagen.',
  createErrVault: 'Im Tresor ist kein Master Key konfiguriert, daher kann das Projekt nicht gespeichert werden.',
  createErrStatus: 'Erstellung fehlgeschlagen ({status})',
  createErrRetry: 'Erstellung fehlgeschlagen, bitte später erneut versuchen.',

  renameErrStatus: 'Umbenennen fehlgeschlagen ({status})',
  renameErrRetry: 'Umbenennen fehlgeschlagen, bitte später erneut versuchen.',

  deleteErrStatus: 'Löschen fehlgeschlagen ({status})',
  deleteErrRetry: 'Löschen fehlgeschlagen, bitte später erneut versuchen.',

  triggerErrNotFound: 'Projekt nicht gefunden, bitte aktualisieren und erneut versuchen.',
  triggerErrStatus: 'Auslösen fehlgeschlagen ({status})',
  triggerErrRetry: 'Auslösen fehlgeschlagen, bitte später erneut versuchen.',
}
