export default {
  "repairCredentialDescription": "Wähle die Repository-Zugangsdaten dieses Projekts erneut. Erst beim Speichern wird es aktualisiert.",
  "title": "Erster erfolgreicher Lauf",
  "intro": "Projekt erstellen, Pipeline vorbereiten und den Lauf selbst bestätigen.",
  "projectLabel": "Aktuelles Projekt",
  "resultProjectLabel": "Projekt des erfolgreichen Laufs",
  "projectUnavailable": "Projektname nicht verfügbar",
  "stepsLabel": "Einrichtungsschritte",
  "steps": {
    "project": "Projekt erstellen",
    "pipeline": "Pipeline vorbereiten",
    "run": "Einmal erfolgreich ausführen"
  },
  "stepDescriptions": {
    "project": "Verbinde dein Repository. Zum Erstellen eines Projekts werden derzeit Repository-Zugangsdaten benötigt.",
    "pipeline": "Speichere die passende Konfiguration und prüfe Zugangsdaten und Laufzeit für tatsächliche Aufgaben. Unbeteiligte Server sind nicht erforderlich.",
    "run": "Sende die Aufgabe im vorhandenen Bestätigungsdialog und prüfe das Pipeline-Ergebnis."
  },
  "stepStates": {
    "saved": "Gespeichert",
    "done": "Erledigt",
    "current": "Aktueller Schritt",
    "pending": "Ausstehend",
    "unknown": "Unbestätigt"
  },
  "states": {
    "loading": "Instanzstatus wird gelesen",
    "unknown": "Noch nicht bestätigbar",
    "create": "Zuerst ein Projekt erstellen",
    "queued": "Lauf in der Warteschlange",
    "running": "Pipeline läuft",
    "waiting_approval": "Warten auf Freigabe",
    "failed": "Letzten Fehler prüfen",
    "configure": "Pipeline weiter vorbereiten",
    "unconfirmed": "Pipeline-Konfiguration prüfen",
    "repository": "Repository-Pipeline prüfen",
    "ready": "Laufbestätigung kann geöffnet werden",
    "success": "Pipeline-Lauf erfolgreich",
    "legacy": "Erfolgreicher Lauf bereits dokumentiert",
    "stub": "Demo-Ausführung schließt die Einrichtung nicht ab",
    "mixed": "Gemischte Ausführung schließt die Einrichtung nicht ab",
    "pending": "Ausführungsnachweis unbestätigt"
  },
  "descriptions": {
    "loading": "Nur Daten dieser Instanz werden gelesen. Überspringen und Rückkehr zur Übersicht bleiben möglich.",
    "unknown": "Der vollständige Zustand konnte nicht bestätigt werden. Das bedeutet nicht, dass Projekte oder erfolgreiche Läufe fehlen. Erneut versuchen.",
    "create": "Nutze das vorhandene Projektformular, wähle Zugangsdaten und gib den Standardbranch an.",
    "queued": "Prüfe die neueste Aufgabe des gewählten Projekts. Erneutes Senden ist nicht erforderlich.",
    "running": "Prüfe Fortschritt und Ergebnis des neuesten Laufs.",
    "waiting_approval": "Öffne die Laufdetails für den Freigabestatus. Die Einrichtung erteilt keine automatische Freigabe.",
    "failed": "Prüfe die Protokolle dieses Laufs und korrigiere dasselbe Projekt vor dem nächsten Lauf.",
    "configure": "Prüfe die zutreffenden fehlenden Angaben unten. Eine Serverregistrierung bestätigt keine Verbindung.",
    "unconfirmed": "Die Bereitschaft ist noch nicht bestätigt. Prüfe die Konfiguration oder nutze den vorhandenen manuellen Einstieg.",
    "repository": "Dieses Projekt verwendet .pipewright.yml im Repository. Die Einrichtung ruft das Repository nicht ab und verlangt keine zusätzliche UI-Konfiguration.",
    "ready": "Öffne den Bestätigungsdialog dieses Projekts. Erst nach deinem Absenden beginnt die Ausführung.",
    "success": "Die Instanz hat einen erfolgreichen Pipeline-Lauf mit echtem Ausführungsnachweis. Das bestätigt nicht zusätzlich Deployment oder Dienstverbindung.",
    "legacy": "Der historische Erfolg erfüllt die Kompatibilitätsregeln. Er bestätigt nicht zusätzlich einen echten Build oder ein Deployment.",
    "stub": "Dieser Erfolg enthält eine Demo-Ausführung und beweist nicht, dass echte Aufgaben funktionieren.",
    "mixed": "Dieser Lauf enthält echte und Demo-Aufgaben und zählt nicht als erster echter Erfolg.",
    "pending": "Der Lauf war erfolgreich, sein Ausführungsnachweis ist jedoch unbestätigt. Echter Erfolg wird nicht abgeleitet."
  },
  "actions": {
    "create": "Projekt erstellen",
    "configure": "Konfiguration fortsetzen",
    "checkPipeline": "Pipeline prüfen",
    "goRun": "Zum Lauf",
    "viewRun": "Lauf ansehen",
    "viewFailed": "Fehlerprotokolle ansehen",
    "edit": "Pipeline bearbeiten",
    "viewResult": "Laufergebnis ansehen",
    "autoTrigger": "Automatische Auslöser konfigurieren",
    "runtimeHelp": "Hilfe zur Ausführungsumgebung",
    "retry": "Erneut versuchen",
    "open": "Diesen Schritt öffnen",
    "servers": "Server registrieren",
    "repairCredential": "Repository-Zugangsdaten ändern",
    "credentials": "Zugangsdaten verwalten"
  },
  "issues": {
    "build": "Prüfe Konfiguration und Werkzeugkette für diesen Build-Pfad.",
    "tasks": "Prüfe Aufgabentypen, Images und Befehle. Der Executor unterstützt möglicherweise nicht alle Aufgaben.",
    "credentials": "Ein von echten Aufgaben verwendeter Zugangsdatenverweis fehlt.",
    "projectCredential": "Die Repository-Zugangsdaten fehlen. Wähle sie im Projekt erneut aus.",
    "vault": "Für den Tresor ist kein Hauptschlüssel konfiguriert. Die Verwendbarkeit vorhandener Zugangsdaten ist unbestätigt.",
    "environment": "Die vom aktuellen Branch referenzierte Umgebung ist nicht definiert.",
    "noTasks": "Der aktuelle Branch enthält keine ausführbaren echten Aufgaben.",
    "notification": "Der von der Benachrichtigungsaufgabe referenzierte Kanal fehlt.",
    "pipeline": "Prüfe die Pipeline-Struktur.",
    "runtime": "Der beim Start gewählte Executor ist im Demo-Modus.",
    "server": "Ein Knoten oder Remote-Runner referenziert einen fehlenden Server.",
    "storage": "Die betreffende Konfiguration konnte nicht gelesen werden. Erneut versuchen."
  },
  "runtime": {
    "stub": "Tatsächlich ist ein Demo-Executor ausgewählt, der keine echten Aufgaben ausführen kann.",
    "available": "Ein echter Executor ist ausgewählt und eine lokale Container-CLI wurde erkannt. Dies bestätigt keine Verbindung zum Container-Daemon.",
    "unknown": "Ausführungsfähigkeit oder Remote-Verbindung sind unbestätigt. Es erfolgen keine automatischen Verbindungsprüfungen.",
    "instructions": "Prüfe Container-CLI, Dienstrechte und Verbindung auf dem Host oder Container von Pipewright. Remote-Runner werden im Variablen-Tab des Projekts konfiguriert. Diese Hilfe installiert nichts, prüft kein SSH und sendet keine Aufgaben."
  }
}
