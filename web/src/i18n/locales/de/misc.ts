export default {
  // ─── shared ──────────────────────────────────────────────────────────────
  loadingShort: 'Wird geladen…',

  // ─── AuditTimeline ───────────────────────────────────────────────────────
  audit: {
    title: 'Audit-Protokoll',
    sub: 'Wer · Wann · Woran',
    treeAria: 'Audit-Zeitleiste',
    emptyLabel: 'Noch keine Audit-Einträge',
    emptyHint: 'Sensible Vorgänge wie das Erstellen, Ändern oder Löschen von Anmeldedaten und Projekten, das Zurücksetzen von Webhook-Secrets und das manuelle Auslösen von Läufen werden hier protokolliert. Die Einträge sind manipulationssicher.',
    loadMore: 'Weitere Audit-Einträge laden →',
    via: 'Web-Konsole',
    actorYou: 'Du',
    verbCreate: 'Erstellt',
    verbUpdate: 'Geändert',
    verbDelete: 'Gelöscht',
    verbReset: 'Zurückgesetzt',
    verbAdd: 'Verbunden',
    verbTrigger: 'Manuell ausgelöst',
    verbDefault: 'Bearbeitet',
    nounCredential: 'Anmeldedaten',
    nounWebhookSecret: 'Webhook-Signatur-Secret',
    nounProject: 'Projekt',
    nounRun: 'Lauf',
    errConnect: 'Verbindung zum Server nicht möglich. Prüfe, ob das Backend läuft, und versuche es erneut',
    errLoad: 'Laden des Audit-Protokolls fehlgeschlagen ({status})',
    errLoadRetry: 'Laden des Audit-Protokolls fehlgeschlagen. Bitte später erneut versuchen',
  },

  // ─── CodeTree / CodeTreeNode ─────────────────────────────────────────────
  tree: {
    aria: 'Code-Verzeichnisbaum',
    fileAria: 'Repository-Dateibaum',
    title: 'Dateien',
    refTitle: 'Aktuelle Ref: {ref}',
    loadingDir: 'Verzeichnis wird geladen…',
    emptyRepo: 'Leeres Repository / Quelltext nicht lesbar',
    emptyDir: 'Leeres Verzeichnis',
    errConnect: 'Verbindung zum Server nicht möglich',
    errNotFound: 'Pfad existiert nicht',
    errLoad: 'Laden fehlgeschlagen ({status})',
    errLoadGeneric: 'Laden fehlgeschlagen',
  },

  // ─── CodeViewer ──────────────────────────────────────────────────────────
  code: {
    viewAria: 'Code-Ansicht',
    editorAria: 'Code-Editor (schreibgeschützt)',
    noFileSelected: 'Keine Datei ausgewählt',
    truncated: 'Gekürzt',
    truncatedTitle: 'Datei zu groß; nur der Anfang wird angezeigt',
    idleTitle: 'Wähle links eine Datei zur Ansicht',
    idleSub: 'Schreibgeschütztes Durchsuchen des Repository-Quelltexts mit Syntaxhervorhebung; Bearbeiten und Committen sind nicht möglich.',
    binaryTitle: 'Binärdatei kann nicht in der Vorschau angezeigt werden',
    degradedTitle: 'Quelltext nicht lesbar',
    degradedSub: 'Das Klonen des Repositorys ist fehlgeschlagen oder die aktuelle Umgebung kann nicht darauf zugreifen. Bitte später erneut versuchen oder die Repository-Einstellungen des Projekts prüfen.',
    errTitle: 'Datei konnte nicht geladen werden',
    fallbackRegionAria: 'Code-Inhalt (Klartext-Fallback)',
    fallbackNote: 'Die Syntaxhervorhebungs-Komponente konnte nicht geladen werden; es wurde auf eine Klartextansicht zurückgegriffen.',
    errConnect: 'Verbindung zum Server nicht möglich',
    errNotFound: 'Datei existiert nicht',
    errLoad: 'Laden fehlgeschlagen ({status})',
    errLoadGeneric: 'Laden fehlgeschlagen',
  },

  // ─── ConfirmDialog ───────────────────────────────────────────────────────
  confirm: {
    cancel: 'Abbrechen',
    confirm: 'Bestätigen',
    typeLabelPrefix: 'Gib',
    typeLabelSuffix: 'zur Bestätigung ein',
    typePlaceholder: '{text} eingeben…',
    typeAria: 'Gib {text} ein, um die Aktion zu bestätigen',
  },

  // ─── EmptyState ──────────────────────────────────────────────────────────
  empty: {
    defaultTitle: 'Keine Daten',
  },

  // ─── ErrorState ──────────────────────────────────────────────────────────
  error: {
    defaultTitle: 'Laden fehlgeschlagen',
    retry: 'Erneut versuchen',
    aiUnavailableAria: 'KI-Funktion nicht verfügbar',
    aiTitle: 'KI-Fehlerdiagnose',
    aiTag: 'Nicht verfügbar',
    aiDesc: 'Der LLM-Anbieter hat nicht geantwortet, daher wurde diesmal keine Diagnose erstellt. Lauf-Ergebnisse und Protokolle werden wie gewohnt aufgezeichnet; das zentrale CI/CD ist nicht betroffen.',
    confidenceLabel: 'Konfidenz {n}% · {level}',
    confidenceHigh: 'Hoch',
    confidenceMedium: 'Mittel',
    confidenceLow: 'Niedrig',
  },

  // ─── ToastHost ───────────────────────────────────────────────────────────
  toast: {
    hostAria: 'Benachrichtigungen',
    itemAria: '{type}-Benachrichtigung: {title}',
    closeAria: 'Benachrichtigung schließen: {title}',
  },
}
