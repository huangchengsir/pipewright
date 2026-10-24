export default {
  deploymentEvidenceUnavailable: "Das Bereitstellungsprotokoll verzeichnet einen Fehler; die Speicherung der Nachweise ist unbestätigt. Der Fehler bleibt bestehen, ohne ungespeicherte Nachweise zu referenzieren.",

  deploymentCompletionPending: "Bereitstellung noch nicht abgeschlossen; der abschließende Beleg steht aus.",
  deploymentCompleted: "Der abschließende Erfolgsbeleg dieser Bereitstellung wurde gelesen.",
  deploymentOutcomeUnavailable: "Kein Erfolgsbeleg vorhanden; das Ergebnis ist nicht verifiziert.",
  deploymentHealthGateFailed: "Die Zustandsprüfung bei der Bereitstellung meldete einen Fehler. Sie wird getrennt vom laufenden Docker-Zustand bewertet.",
  deploymentHealthGate: "Zustandsprüfung bei Bereitstellung",

  localImageMatched: "Der Container verwendet die vor dieser Aktivierung fixierte lokale Image ID.",
  localImageUnexpected: "Nach der Bereitschaftsfrist wurde ein anderes lokales Image bestätigt.",
  localImageUnavailable: "Die lokale Image-Identität kann nicht geprüft werden.",
  localImageInvalid: "Lokale Identität oder Beleg der Fixierung vor dem Start ist ungültig.",
  localImagePending: "Das erwartete Image wird nach dem Build vor dem Start erfasst. Dieser Plan enthält noch keine Image ID.",
  localImageFixed: "Die lokale Image ID wurde vor der Aktivierung fixiert.",
  expectedImage: "Erwartete Image-Identität",

  healthConditionChanged:
    "Die Docker-Zustandsprüfung hat sich geändert; die ursprünglichen Bedingungen werden nicht erneut geprüft.",
  healthComparisonUnknown:
    "Die ursprüngliche Zustandsprüfung ist nicht verifizierbar; gleiche Bedingungen sind nicht belegt.",
  identityMissing: "Eine prüfbare Container- oder Verbindungsidentität fehlt.",
  digestMissing: "Der Repository-Digest kann nicht geprüft werden.",
  targetChanged:
    "Zielidentität oder Konfiguration geändert; die Bindung kann nicht fortgesetzt werden.",
  replaced:
    "Eine spätere Bereitstellung ersetzte das Ziel und beendete das alte Fenster.",
  notFound: "Der gebundene Container existiert nicht mehr.",
  exited: "Der Container wurde beendet oder durch OOM gestoppt.",
  restarted: "Ein Neustart wurde beobachtet.",
  versionMatched: "Der erwartete Repository-Digest stimmt überein.",
  oldVersion:
    "Nach der Bereitschaftsfrist wurde eine andere Version bestätigt.",
  restartStable: "Keine Zunahme der Neustarts beobachtet.",
  running: "Der Container lief zum Zeitpunkt dieser Probe.",
  healthGrace: "Die Health-Startkulanz läuft noch.",
  healthy: "Docker meldet healthy.",
  unhealthy: "Docker meldet unhealthy.",
  healthUnknown: "Der Docker-Health-Status ist unklar.",
  healthAbsent:
    "Kein Docker-Healthcheck; fachliche Funktionsfähigkeit ist nicht geprüft.",
  collectionUnavailable:
    "Keine gültige Probe erfasst. Dies allein belegt keinen Anwendungsfehler.",
  truncated: "Das erfasste Ergebnis wurde gekürzt.",
  deadline: "Die Beobachtungsfrist wurde erreicht.",
  userStopped: "Der Benutzer hat den Stopp angefordert.",
  interrupted: "Der Dienst hat die Beobachtung unterbrochen.",
  persistenceError: "Nachweise konnten nicht gespeichert werden.",
  runtimeSample: "Strukturierte Laufzeitprobe",
  baselineSample: "Probe unmittelbar vor der Bereitstellung",
  durationChanged: "Beobachtungsdauer geändert.",
  logChanged: "Protokollumfang geändert.",
  targetsChanged: "Zielumfang geändert.",
  environmentChanged: "Umgebung geändert.",
  rolledBack:
    "Bereitstellung zurückgerollt; erwartete neue Version nicht bestätigt.",
  outcomeUnknown: "Bereitstellungsergebnis unbekannt.",
  deploymentFailed: "Bereitstellung fehlgeschlagen.",
  pendingActivation:
    "Die tatsächliche Containeridentität wird nach Aktivierung gebunden.",
};
