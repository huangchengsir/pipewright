export default {
  // ─── Container-Status ───
  stateRunning: 'Läuft',
  statePaused: 'Pausiert',
  stateRestarting: 'Neustart',
  stateCreated: 'Erstellt',
  stateExited: 'Beendet',
  stateDead: 'Fehlerhaft',
  stateUnknown: 'Unbekannt',

  // ─── Aktionsschaltflächen für den Container-Lebenszyklus ───
  actionStart: 'Starten',
  actionRestart: 'Neu starten',
  actionStop: 'Stoppen',
  actionPause: 'Pausieren',
  actionUnpause: 'Fortsetzen',
  actionKill: 'Kill',
  actionRm: 'Löschen',

  // ─── Hover-Hinweise der Aktionsschaltflächen ───
  hintStart: 'Startet einen gestoppten Container (docker start) und führt einen neuen Prozess von Grund auf aus.',
  hintRestart: 'Neu starten: zuerst ordnungsgemäß stoppen (SIGTERM, 10 s Kulanzzeit), dann starten (docker restart).',
  hintStop: 'Ordnungsgemäßes Stoppen: SIGTERM senden und, falls der Container nicht innerhalb von 10 s beendet wird, SIGKILL nachschieben (docker stop). Für das tägliche Stoppen von Diensten verwenden, damit das Programm aufräumen und auf die Festplatte schreiben kann.',
  hintPause: 'Pausieren: friert per cgroup alle Prozesse im Container ein (docker pause); der Speicher bleibt unverändert erhalten und die CPU wird ihm nicht mehr zugeteilt. Klicke auf „Fortsetzen“, um an der unterbrochenen Stelle weiterzumachen. Gibt keinen Speicher frei.',
  hintUnpause: 'Fortsetzen: taut einen pausierten Container auf (docker unpause); derselbe Prozess macht an der unterbrochenen Stelle weiter.',
  hintKill: 'Erzwungenes Kill: sendet direkt SIGKILL, um sofort und ohne Gelegenheit zum Aufräumen zu beenden (docker kill); nicht geschriebene Daten können verloren gehen. Nur verwenden, wenn „Stoppen“ hängt.',
  hintRm: 'Löscht den Container (docker rm); ein laufender muss zuerst gestoppt werden. Die Container-Konfiguration wird entfernt, eingehängte Datenvolumes bleiben jedoch unberührt.',

  // ─── Bestätigung destruktiver Aktionen ───
  dangerRestartTitle: 'Container {n} neu starten?',
  dangerRestartBody: 'Der Container wird gestoppt und neu gestartet; sein Dienst ist währenddessen kurzzeitig nicht verfügbar.',
  dangerRestartConfirm: 'Neustart bestätigen',
  dangerStopTitle: 'Container {n} stoppen?',
  dangerStopBody: 'Der Container wird gestoppt, wodurch der von ihm bereitgestellte Dienst bis zum erneuten Start unterbrochen wird.',
  dangerStopConfirm: 'Stoppen bestätigen',
  dangerKillTitle: 'Container {n} erzwungen killen?',
  dangerKillBody: 'Es wird SIGKILL gesendet, um den Container-Prozess sofort zu beenden; nicht geschriebene Daten können verloren gehen.',
  dangerKillConfirm: 'Erzwungenes Kill',
  dangerRmTitle: 'Container {n} löschen?',
  dangerRmBody: 'Der Container wird gelöscht (ein laufender muss zuerst gestoppt werden). Seine Konfiguration wird mit ihm entfernt; Datenvolumes bleiben unberührt.',
  dangerRmConfirm: 'Löschen bestätigen',

  // ─── Parametertypen ───
  paramTypeString: 'Text',
  paramTypeChoice: 'Aufzählung',
  paramTypeBoolean: 'Boolesch',
  paramTypeNumber: 'Zahl',

  // ─── Validierung von Parameterwerten ───
  paramRequired: 'Der Parameter „{label}“ ist erforderlich',
  paramNotNumber: 'Der Parameter „{label}“ muss eine Zahl sein',
  paramNotBoolean: 'Der Parameter „{label}“ muss true/false sein',
  paramNotInChoice: 'Der Parameter „{label}“ ist nicht unter den verfügbaren Optionen',

  // ─── Promotion-Status ───
  promotionPromoted: 'Hochgestuft',
  promotionPending: 'Genehmigung ausstehend',
  promotionRejected: 'Abgelehnt',

  // ─── Validierung des Umgebungsnamens ───
  envNameEmpty: 'Der Umgebungsname darf nicht leer sein',
  envNameInvalid: 'Der Umgebungsname darf nur Buchstaben, Ziffern, Bindestriche und Unterstriche enthalten',
  envNameTooLong: 'Der Umgebungsname darf 64 Zeichen nicht überschreiten',

  // ─── Nebenläufigkeitslimit ───
  concurrencyNotInteger: 'Das Nebenläufigkeitslimit muss eine Ganzzahl sein',
  concurrencyTooSmall: 'Das Nebenläufigkeitslimit darf nicht kleiner als {min} sein',
  concurrencyTooLarge: 'Das Nebenläufigkeitslimit darf {max} nicht überschreiten',
  concurrencyUnlimited: 'Unbegrenzt',

  // ─── Terminal ───
  terminalSessionEnded: 'Die Terminalsitzung wurde beendet',
}
