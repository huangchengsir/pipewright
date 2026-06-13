export default {
  // ─── header ────────────────────────────────────────────────────
  brandTitle: 'Pipewright · Bibliothek',
  brandSubtitle: 'Studio für benutzerdefinierte Knoten (Custom Node Studio)',
  namePlaceholder: 'Diesen Knoten benennen…',
  nameAria: 'Knotenname',
  cancel: 'Abbrechen',
  saving: 'Wird gespeichert…',
  saveToLibrary: 'In Bibliothek speichern',

  // ─── hero ──────────────────────────────────────────────────────
  heroEyebrow: 'Low-Code · Einmal definieren, überall wiederverwenden',
  heroTitlePre: 'Stelle parametrisierbare Schritte per ',
  heroTitleEm: 'Ziehen',
  heroTitlePost: ' zu einem wiederverwendbaren Knoten zusammen',
  heroDescPre: 'Ziehe Blöcke aus der gruppierten Palette links in die mittlere Leinwand und sortiere die Karten um; rechts „beförderst“ du Variablen zu Knoten-Oberflächenparametern. Unten wird in Echtzeit in einen vorhandenen',
  heroDescPost: 'Knoten kompiliert — keine Backend-Änderungen, Instanzen konfigurieren nur die wenigen beförderten.',

  // ─── banners ───────────────────────────────────────────────────
  loadingBanner: 'Wird geladen…',
  loadFailed: 'Laden fehlgeschlagen',
  loadFailedCode: 'Laden fehlgeschlagen ({code})',
  saveFailed: 'Speichern fehlgeschlagen',
  saveFailedCode: 'Speichern fehlgeschlagen ({code})',
  errNameRequired: 'Der Knotenname darf nicht leer sein',
  errNeedCommandStep: 'Mindestens ein Schritt muss einen Befehl erzeugen',
  updatedToast: 'Benutzerdefinierter Knoten „{name}“ aktualisiert',
  createdToast: 'Benutzerdefinierter Knoten „{name}“ erstellt',

  // ─── palette ───────────────────────────────────────────────────
  paletteHintPre: 'Ziehe die Blöcke unten ',
  paletteHintStrong: 'in',
  paletteHintPost: ' die mittlere Leinwand (oder klicke, um am Ende anzuhängen).',

  // ─── compose canvas ────────────────────────────────────────────
  composeTitle: 'Schritt-Komposition · Karten zum Umsortieren ziehen',
  composeEmpty: 'Ziehe Blöcke hierher, um zu beginnen →',
  moveUp: 'Nach oben',
  moveDown: 'Nach unten',
  deleteStep: 'Löschen',

  // ─── step field labels ─────────────────────────────────────────
  fieldCommandMultiline: 'Befehl (mehrzeilig)',
  fieldInstallCommand: 'Installationsbefehl',
  fieldEchoText: 'Echo-Text',
  phEchoText: 'Build wird gestartet…',
  fieldEnvKey: 'Variablenname',
  fieldEnvValue: 'Wert',
  fieldTargetDir: 'Zielverzeichnis',
  fieldPathDir: 'An PATH anzuhängendes Verzeichnis',
  fieldArtifactPath: 'Artefaktpfad (Glob)',
  fieldSaveAs: 'Speichern unter',
  fieldArchiveFile: 'Archivdatei',
  fieldExtractTo: 'In Verzeichnis entpacken',
  fieldCondition: 'Shell-Bedingung (überspringt den Rest, wenn falsch; set -e-sicher)',
  fieldCommand: 'Befehl',
  fieldRetryCount: 'Anzahl',
  fieldDelaySecs: 'Intervall (Sek.)',
  fieldTimeoutSecs: 'Timeout (Sek.)',
  fieldSleepSecs: 'Wartezeit (Sek.)',
  fieldProbeUrl: 'Probe-URL',
  fieldNote: 'Notiz (wird als #-Kommentar kompiliert, nicht ausgeführt)',
  fieldTestCommand: 'Testbefehl',
  fieldReportPath: 'Berichtspfad (JUnit)',
  fieldMinCoverage: 'Abdeckungsschwelle %',

  // ─── right rail tabs ───────────────────────────────────────────
  tabParams: 'Beförderte Parameter',
  tabMeta: 'Knotenoberfläche',

  // ─── params tab ────────────────────────────────────────────────
  paramsHintPre: 'In Schritten referenziert als',
  paramsHintPost: '; bei der Instanz-Wiederverwendung werden nur diese konfiguriert.',
  paramsEmpty: 'Noch keine beförderten Parameter. Das gesamte Skript fest zu codieren ist in Ordnung; befördere einen Wert, um ihn pro Instanz änderbar zu machen.',
  removeParamAria: 'Parameter entfernen',
  newParamLabel: 'Neuer Parameter',
  phDisplayLabel: 'Anzeigebezeichnung',
  phDefaultValue: 'Standardwert',
  phOptions: 'Kommagetrennte Optionen, z. B. 20, 18, 22',
  addParam: '＋ Einen Parameter befördern',

  // ─── param type options ────────────────────────────────────────
  paramTypeText: 'Text',
  paramTypeSelect: 'Aufzählung',
  paramTypeNumber: 'Zahl',
  paramTypeToggle: 'Boolesch',

  // ─── meta tab ──────────────────────────────────────────────────
  metaImagePre: 'Laufzeit-Image (kann ',
  metaImagePost: ' enthalten)',
  metaIcon: 'Symbol',
  metaCategory: 'Kategorie',
  phCategory: 'Build & Artefakte',
  metaSummaryPre: 'Einzeilige Zusammenfassung (kann ',
  metaSummaryPost: ' enthalten)',
  metaHint: 'Die Kategorie bestimmt, in welcher Gruppe er im „Knoten hinzufügen“-Auswahlmenü erscheint; Zusammenfassung + Symbol sind die Karte, die Wiederverwender zuerst sehen.',
  defaultCategory: 'Benutzerdefiniert',

  // ─── bottom: compiled output ───────────────────────────────────
  compiledTitle: 'Kompilierte Ausgabe · templated Knoten-config (Backend führt sie unverändert aus)',
  undeclaredWarn: '⚠ Schritte referenzieren nicht beförderte Parameter: {refs} (unverändert beibehalten, pro Instanz nicht änderbar)',
  compiledComment: '# templated benutzerdefinierte Knoten-config —— Backend führt sie nach renderTemplate({open}) im Container aus',
  compiledEmpty: '(Noch keine Schritte)',

  // ─── bottom: instance preview ──────────────────────────────────
  previewTitle: 'Instanz-Vorschau · Mehr siehst du nicht, nachdem du ihn in eine Pipeline gezogen hast',
  unnamedNode: 'Unbenannter Knoten',
  customLabel: 'Benutzerdefiniert',
  previewNote: 'Genau das ist das n8n-Paradigma „Parameter befördern → kurze Instanzliste“ / Node-RED-Subflow-Eigenschaften: Wiederverwender müssen das interne Skript nicht verstehen, sondern nur die freigegebenen Parameter konfigurieren.',
}
