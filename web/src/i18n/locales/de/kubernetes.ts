export default {
  groups: {
    resources: "Ressourcen & Auslieferung",
    integrations: "Integrationen & KI",
    security: "Sicherheit & Konto",
    system: "System & Benachrichtigungen",
  },
  close: "Schließen",
  cancel: "Abbrechen",
  confirm: "Bestätigen und fortfahren",
  save: "Speichern",
  edit: "Bearbeiten",
  delete: "Löschen",
  remove: "Entfernen",
  name: "Name",
  select: "Auswählen…",
  none: "Keine",
  loading: "Wird geladen…",
  enabled: "Aktiv",
  disabled: "Deaktiviert",
  enable: "Aktivieren",
  disable: "Deaktivieren",
  credentials: "Kubernetes-Zugangsdaten",
  addCredential: "Kubernetes-Zugangsdaten hinzufügen",
  rotate: "Zugangsdaten erneuern",
  authentication: "Authentifizierung",
  serverToken: "Server und Token",
  context: "Kontext",
  server: "API-Server-URL",
  ca: "CA-Zertifikat (PEM)",
  token: "Zugriffstoken",
  tlsName: "TLS-Servername (optional)",
  preview: "Format prüfen",
  credential: "Zugangsdaten",
  identity: "Technische Details",
  addConnection: "Verbindung hinzufügen",
  editConnection: "Verbindung bearbeiten",
  emptyConnections: "Cluster verbinden",
  manageCredentials: "Cluster-Zugangsdaten verwalten",
  grants: "Erlaubte Ziele",
  addGrant: "Berechtigung hinzufügen",
  grantTitle: "Zielberechtigung konfigurieren",
  project: "Projekt",
  environment: "Umgebung",
  namespace: "Namespace",
  deployment: "Deployment",
  container: "Container",
  containers: "Erlaubte Container",
  executor: "Ausführungsstelle",
  executors: "Erlaubte Ausführungsstellen",
  local: "Lokales Docker",
  sshServer: "SSH-Server",
  addSSH: "SSH-Ausführungsstelle hinzufügen",
  hostKey: "SSH-Host-Fingerabdruck",
  profile: "Werkzeugprofil",
  toolImage: "Werkzeug-Image (fester Digest)",
  architecture: "Architektur",
  kubectlPath: "kubectl-Pfad",
  registryCredential: "Registry-Zugangsdaten (optional)",
  approvalRequired: "Freigabe erforderlich",
  approvalOptional: "Pipeline-Freigaberegel verwenden",
  probe: "Verbindung prüfen",
  replicas: "Bereite Replikate",
  target: "Auslieferungsziel",
  connection: "Cluster-Verbindung",
  manageConnections: "Verbindungen und Berechtigungen verwalten",
  jobTitle: "Kubernetes-Auslieferung",
  imageSource: "Image-Quelle",
  more: "Weitere Optionen",
  timeout: "Bereitschaftsfrist (Sekunden)",
  passwordTitle: "Identität bestätigen",
  password: "Aktuelles Kontopasswort",
  unsaved: "Ungespeicherte Änderungen verwerfen?",
  discard: "Änderungen verwerfen",
  receipt: "Cluster-Auslieferungsnachweis",
  reconcile: "Cluster-Zustand prüfen",
  retire: "Lokale Verfolgung beenden",
  retired: "Verfolgung beendet",
  operation: "Vorgangs-ID",
  sourceRun: "Quelllauf",
  intent: "Hash der Auslieferungsabsicht",
  revision: "Revision",
  reason: "Diagnosecode",
  phases: "Auslieferungsschritte",
  recorded: "Aufgezeichnet",
  notRecorded: "Nicht aufgezeichnet",
  credentialsHint: "Cluster-Anmeldung, getrennt von Git- und SSH-Zugangsdaten.",
  emptyCredentials: "Keine Kubernetes-Zugangsdaten konfiguriert.",
  secretHint:
    "Geheimnisse werden nach Speichern oder Abbrechen gelöscht. Zur Erneuerung vollständige Daten eingeben; keine Klartextanzeige.",
  previewReady: "Format gültig; Cluster-Verbindung nicht getestet",
  connectionsHint:
    "In vorhandene Deployments mit vertrauenswürdigen Verbindungen und genauen Berechtigungen ausliefern.",
  setupHint:
    "Zugangsdaten im Tresor hinzufügen, dann Werkzeug-Image und erlaubte Ziele festlegen.",
  grantHint:
    "Nur dieses Projekt, diese Umgebung, Workload, Container und Ausführungsstelle erlauben.",
  emptyGrants:
    "Keine erlaubten Ziele. Vor Prüfung oder Auslieferung Berechtigung hinzufügen.",
  containersHint: "Namen durch Kommas trennen. Keine Platzhalter.",
  hostKeyHint:
    "SHA256-Fingerabdruck über vertrauenswürdigen Kanal prüfen. Exakte Übereinstimmung erforderlich.",
  profileHint:
    "Speichern prüft das Werkzeug nicht. Verbindung prüfen, um die Laufzeit zu validieren.",
  probeHint:
    "Nur das gewählte erlaubte Ziel lesen. Keine Image-Änderung oder Auslieferung.",
  probeReady: "Verbindung geprüft; Ziel gelesen",
  jobDescription:
    "Image eines vorhandenen Deployments aktualisieren und Bereitschaft abwarten.",
  separateStage:
    "Eine eigene Phase ohne Matrix, parallele Knoten oder Zusatzaufgaben ist erforderlich. Über Knoten hinzufügen erstellen.",
  noProjectGrant:
    "Keine Berechtigung für dieses Projekt. Zuerst in den Einstellungen hinzufügen.",
  staleTarget: "Bisheriges Ziel nicht verfügbar; Berechtigungen prüfen",
  unavailableChoice: "Bisherige Auswahl nicht verfügbar",
  imageSourceHint:
    "Vorgelagerten Image-Push-Knoten wählen. Dessen tatsächlicher Registry-Digest wird verwendet.",
  noPushSource:
    "Kein vorgelagerter Image-Push-Knoten. Zuerst hinzufügen und verbinden.",
  passwordHint:
    "Für diese Aktion muss das Kontopasswort innerhalb der letzten 5 Minuten bestätigt sein.",
  unsavedBody: "Beim Verlassen wird das Formular geleert.",
  deleteBody:
    "„{name}“ löschen? Noch verwendete Objekte können nicht gelöscht werden.",
  noReceipt:
    "Kein eindeutiger Nachweis für diesen Knoten. Erfolg und Wiederholbarkeit sind unbestätigt.",
  retireBody:
    "Beendet die lokale Verfolgung für eine neue vollständige Auslieferung. Kein Rollback und keine Erfolgskorrektur des ursprünglichen Laufs.",
  unknownHint:
    "Die Auslieferung könnte bereits wirksam sein. Erst prüfen, nicht erneut senden.",
  cleanupHint:
    "Bereinigung des Werkzeugcontainers unbestätigt; Wiederholung gesperrt.",
  historyHint:
    "Prüfungen ändern den ursprünglichen Lauf nicht. Spätere Läufe müssen das Ziel erneut prüfen.",
  notRecordedHint:
    "Kein Nachweis für diesen Schritt; Beginn oder Erfolg sind unbestätigt.",
  noRollback:
    "Dieser Verlauf enthält Kubernetes-Ziele; kein SSH-Rollback. Gewünschte Version über einen neuen vollständigen Lauf ausliefern.",
  adopted: "Quellauslieferung erneut geprüft und verwendet",
  rolloutOnly:
    "Nur Rollout-Bereitschaft geprüft, keine fortlaufende Überwachung.",
  phase: {
    preflight: "Vorprüfung",
    update: "Image aktualisieren",
    rollout: "Bereitschaft abwarten",
    cleanup: "Werkzeuge bereinigen",
  },
  phaseCode: {
    patch_not_authorized:
      "Diese Cluster-Zugangsdaten dürfen dieses Deployment nicht aktualisieren. Bitten Sie einen Administrator, die Berechtigungen zu prüfen. Es wurde kein Update gesendet.",
    permission_check_unverified:
      "Die Berechtigung für die Cluster-Bereitstellung konnte nicht geprüft werden. Prüfen Sie die Verbindung und die Konfiguration der Berechtigungsprüfung. Es wurde kein Update gesendet.",
    preflight_started: "Berechtigungen, Werkzeug und Ziel werden geprüft.",
    image_update_dispatched:
      "Image-Aktualisierung gesendet; der Nachweis bestimmt das Ergebnis.",
    rollout_observing: "Bereitschaft der Zielreplikate wird beobachtet.",
    rollout_ready: "Diese Beobachtung hat die Bereitschaft bestätigt.",
    cleanup_started: "Werkzeugcontainer dieses Vorgangs wird bereinigt.",
    cleanup_verified: "Werkzeugbereinigung bestätigt.",
    cleanup_unverified: "Werkzeugbereinigung unbestätigt; Nachweis prüfen.",
  },
  business: {
    not_dispatched: "Auslieferung wird vorbereitet",
    submitted: "Übermittelt, wartet auf Bereitschaft",
    ready: "Workload bereit",
    unchanged: "Version bereits vorhanden, Bereitschaft bestätigt",
    failed: "Auslieferung unvollständig",
    superseded: "Ziel durch andere Änderung ersetzt",
    outcome_unknown: "Auslieferungsergebnis unbestätigt",
  },
  cleanup: {
    pending: "Bereinigung ausstehend",
    verified: "Bereinigung bestätigt",
    blocked: "Bereinigung blockiert",
  },
  errors: {
    invalid: "Ungültige Konfiguration. Felder prüfen.",
    session: "Sitzung abgelaufen. Erneut anmelden.",
    passwordRequired: "Vor dieser Aktion Passwort erneut bestätigen.",
    passwordInvalid:
      "Passwortprüfung fehlgeschlagen. Prüfen und erneut versuchen.",
    denied: "Verbindung oder Ziel nicht autorisiert.",
    missing: "Eintrag nicht gefunden. Liste aktualisieren.",
    conflict:
      "Konfiguration anderweitig geändert. Entwurf erhalten; vor Speichern aktualisieren und prüfen.",
    inUse: "Eintrag wird noch verwendet und kann nicht geändert werden.",
    held: "Unvollständige Auslieferung belegt das Ziel. Ursprünglichen Vorgang prüfen.",
    ownerUnknown:
      "Ende der ursprünglichen Ausführung unbestätigt. Übernahme und Wiederholung gesperrt.",
    runtime:
      "Laufzeit-Sicherheitsprüfung fehlgeschlagen. Werkzeug und Voraussetzungen prüfen.",
    credentialUnavailable:
      "Cluster-Zugangsdaten nicht verfügbar. Tresor und Verbindung prüfen.",
    observation:
      "Beobachtung unterbrochen; Ergebnis unbestätigt. Ursprünglichen Vorgang prüfen.",
    unavailable:
      "Ausführungsdienst nicht verfügbar. Verbindungsvoraussetzungen prüfen.",
    uncertain:
      "Ergebnis ungewiss; erneutes Senden gesperrt. Einträge aktualisieren. Ein gleicher Name beweist keinen Erstellungserfolg.",
    load: "Laden fehlgeschlagen. Zum Wiederholen aktualisieren.",
  },
  readBack: "Einträge nur lesend prüfen",
  acknowledge: "Geprüft; Bearbeitung beenden",
  readBackHint:
    "Aktuelle Einträge geladen. Identität und Revision prüfen; Ergebnis des ursprünglichen Auftrags bleibt unbestätigt.",
  targetCount: "{n} Auslieferungsziele",
};
