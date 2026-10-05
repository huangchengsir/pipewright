export default {
  "title": "Pipeline-Anleitung",
  "locate": "Bedienelement finden",
  "review": "Konfiguration eingegeben, weiter",
  "refresh": "Erneut prüfen",
  "close": "Anleitung beenden",
  "unsaved": "Der Entwurf ist nicht gespeichert. Beim Beenden bleiben Änderungen erhalten.",
  "phases": {
    "loading": {
      "title": "Konfiguration prüfen",
      "description": "Nur diese Instanz wird gelesen."
    },
    "unknown": {
      "title": "Status unbestätigt",
      "description": "Erneut prüfen. Unbekannt bedeutet nicht bereit."
    },
    "repository": {
      "title": "Repository prüfen",
      "description": "Dieses Projekt nutzt .pipewright.yml. Kein zusätzlicher UI-Speichervorgang oder automatischer Abruf."
    },
    "stage": {
      "title": "Aufgabenphase hinzufügen",
      "description": "Auf dem Canvas eine Phase hinzufügen. Neben der Quelle ist eine tatsächliche Aufgabe nötig."
    },
    "task": {
      "title": "Erste Aufgabe hinzufügen",
      "description": "Unter der Phase den seriellen Knoten anklicken und die Aufgabenauswahl öffnen."
    },
    "choose": {
      "title": "Aufgabentyp wählen",
      "description": "Einen passenden Typ wählen. Benutzerdefiniertes Skript eignet sich zum Einstieg; Image und Befehle selbst eingeben."
    },
    "configure": {
      "title": "Aufgabe konfigurieren",
      "description": "Die Aufgabe öffnen und rechts die nötigen Werte eingeben. Skripte benötigen Image und Befehle. Danach weiter."
    },
    "save": {
      "title": "Speichern und prüfen",
      "description": "Im Kopfbereich den Entwurf speichern. Danach werden fehlende Angaben geprüft; Eingaben allein bedeuten nicht bereit."
    },
    "issues": {
      "title": "Gespeichert, Angaben fehlen",
      "description": "Einträge führen zur Konfiguration. Wenn keine Aufgabe zum Branch passt, Phasenbedingungen und Branch-Zuordnung prüfen und speichern."
    },
    "runtime": {
      "title": "Ausführungsumgebung vorbereiten",
      "description": "Gespeichert, aber die Ausführung ist nicht bereit. Erneutes Speichern behebt keinen Demo-Executor."
    },
    "ready": {
      "title": "Bereit zur Laufbestätigung",
      "description": "Konfiguration, Referenzen und Executor sind geprüft. Die Aktion öffnet nur die Bestätigung; Verbindungen sind nicht nachgewiesen."
    }
  }
}
