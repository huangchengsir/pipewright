export default {
  aiTitle: 'KI-Auswertung',
  boundaryDetails: 'Berichtsgrenzen und Aufbewahrung der Belege',
  titles: {
    notFound: 'Container zum Stichprobenzeitpunkt nachweislich nicht vorhanden',
    unknown: 'Dieser Beleg kann noch nicht ausgewertet werden',
    unavailable: 'Keine gültige Laufzeitstichprobe erhalten',
    running: 'Container lief zum Stichprobenzeitpunkt',
    exited: 'Container lief zum Stichprobenzeitpunkt nicht',
    oom: 'Beendigung wegen Speichermangels aufgezeichnet',
    image: 'Erwartete Image-Identität aufgezeichnet',
    gateFailed: 'Zustandsprüfung bei Bereitstellung fehlgeschlagen',
    logs: 'Begrenzte Zeitfensterprotokolle gespeichert',
    unhealthy: 'Docker-Zustandsprüfung zum Stichprobenzeitpunkt fehlgeschlagen',
    logsUnavailable: 'Keine Zeitfensterprotokolle erhalten',
    logsPartial: 'Nur ein Teil der Zeitfensterprotokolle gespeichert',
    logsEmpty: 'Protokollzeitfenster erfasst; keine Zeilen zurückgegeben',
  },
  logExcerpt:
    'Protokollauszug (bis zu 600 Zeichen; vollständiger Inhalt in den technischen Details)',
  snapshotScope:
    'Wertet nur den für jede Übermittlung ausgewählten und bestätigten Schnappschuss aus. Analysiert nicht den gesamten Bericht und untersucht nicht automatisch.',
  nextTitle: 'Nächste Schritte',
  nextDescription:
    'Prüfen Sie die Fehlerbelege vor einer Untersuchung oder erneuten Beobachtung.',
  investigateAction: 'Zugehörige Änderungen untersuchen',
  why: 'Warum die Prüfung fehlgeschlagen ist',
  failedFallback:
    'Die Regeln bestätigen einen Fehler. Genaue Bedingungen fehlen; prüfen Sie die technischen Daten.',
  unknownTitle: 'Noch ungeklärt',
  unknownScope:
    'Stichproben belegen weder dauerhafte Verfügbarkeit noch fachliche Funktionsfähigkeit. Die vollständige Ursache erfordert unabhängige Belege.',
  unconfirmed:
    'Ungeklärte Angabe; der ursprüngliche Datensatz bleibt in den technischen Details erhalten.',
  unknown: 'Unbekannt',
  unknownTarget: 'Unbestätigtes Ziel',
  unknownCondition: 'Unbekannte Prüfbedingung',
  reportMissing: 'Dieser Bericht wurde nicht gefunden.',
  technicalDetails: 'Technische Details',
  reportDetails: 'Ursprüngliche Berichtsdaten',
  evidenceReference: 'Beleg {n} anzeigen',
  evidenceLoaded: '{n} geladen; gezählt werden nur bereits geladene Belege.',
  continueFinding: 'Diesen Beleg weitersuchen',
  find: {
    found: 'Der zitierte Beleg wurde gefunden.',
    pending:
      'Der Beleg ist noch nicht geladen. Sie können seitenweise weitersuchen.',
    loading: 'Belege werden geladen und gesucht…',
    missing:
      'Die letzte Seite ist erreicht. Dieser Beleg wurde nicht gefunden.',
    expired:
      'Die Belege sind abgelaufen. Das historische Ergebnis bleibt erhalten.',
    error:
      'Die Belege konnten nicht gelesen werden. Setzen Sie die Suche manuell fort.',
    stalled:
      'Die Seitenposition hat sich nicht verändert. Die Suche wurde angehalten; aktualisieren Sie den Bericht.',
  },
  kinds: {
    runtime: 'Laufzeitstichprobe',
    baseline: 'Ausgangswert vor Bereitstellung',
    expected_image: 'Erwarteter Image-Datensatz',
    deployment_health_gate: 'Zustandsprüfung bei Bereitstellung',
    logs: 'Protokolle des Zeitfensters',
    unknown: 'Unbekannter Belegtyp',
  },
  running: 'Laufzustand',
  runningValue: 'Läuft',
  stoppedValue: 'Gestoppt',
  exitCode: 'Exit-Code',
  oom: 'Beendigung wegen Speichermangels',
  restarts: 'Anzahl der Neustarts',
  yes: 'Ja',
  no: 'Nein',
  health: 'Docker-Zustand',
  healthValues: {
    healthy: 'Docker-Prüfung bestanden',
    unhealthy: 'Docker-Prüfung fehlgeschlagen',
    starting: 'Docker-Prüfung startet',
    absent: 'Nicht eingerichtet; fachlicher Zustand unbekannt',
    unknown: 'Unbekannt',
  },
  imageIdentity: 'Image-Identität',
  imageRecorded: 'Identitätswert',
  recorded: 'Aufgezeichnet; siehe technische Details',
  identityValues: {
    config_id: 'Lokale Docker-Image-ID',
    manifest: 'Manifest-Prüfsumme',
    index: 'Index-Prüfsumme',
    repository_digest_unknown: 'Art der Registry-Prüfsumme unbekannt',
    unknown: 'Unbekannt',
  },
  gate: 'Bereitstellungsprüfung',
  failed: 'Fehlgeschlagen',
  windowStart: 'Beginn des Protokollzeitfensters',
  windowEnd: 'Ende des Protokollzeitfensters',
  logGap: 'Lücke im Zeitplan der Protokollerfassung',
  fileStatus: {
    added: 'Hinzugefügt',
    modified: 'Geändert',
    deleted: 'Gelöscht',
    renamed: 'Umbenannt',
    copied: 'Kopiert',
    unknown: 'Unbekannter Status',
  },
  timeline: {
    deploymentStarted: 'Bereitstellungsversuch gestartet',
    activationStarted: 'Container-Aktivierung gestartet',
    deploymentFinished: 'Bereitstellungsversuch beendet',
    baselineFailing:
      'Ausgangswert vor Bereitstellung meldete bereits einen Laufzeitfehler',
    logsRequested: 'Begrenzte Protokollerfassung angefordert',
    logsCollected: 'Ergebnis der Zeitfenster-Protokollerfassung gespeichert',
    logsUnavailable: 'Zeitfensterprotokolle nicht verfügbar',
    finished: 'Beobachtungsaufgabe beendet',
    investigated: 'Änderungsuntersuchung aktualisiert',
    evidenceExpired: 'Ursprüngliche Belege abgelaufen',
  },
  runtimeImageTruncated:
    'Image-Metadaten wurden gekürzt. Erfasste Container-Laufzeitdaten bleiben erhalten; die vollständige Image-Identität ist ungeklärt.',
  targetBindingChanged:
    'Die Zielkonfiguration wurde nach der Erfassung geändert. Diese Laufzeitdaten bestätigen nicht die Gültigkeit der aktuellen Zielbindung.',
  targetBindingUnknown:
    'Das Kennzeichen für Zieländerungen ist ungültig. Die aktuelle Zielbindung ist ungeklärt.',
  logsPartialScope:
    'Nur der gespeicherte Auszug ist verfügbar. Im angeforderten Zeitfenster können Inhalte fehlen oder gekürzt sein.',
  logLines: 'Erfasste Protokollzeilen',
  reasons: {
    sourceCacheBusy:
      'Der lokale Quelltextcache ist belegt; ein Quelltextvergleich liegt nicht vor.',
    sourceCacheUnreadable:
      'Der lokale Quelltextcache ist nicht lesbar; der Vergleich ist ungeklärt.',
    baseCommitNotCached:
      'Der Ausgangscommit ist nicht lokal gespeichert; ein genauer Vergleich ist nicht möglich.',
    currentCommitNotCached:
      'Der aktuelle Commit ist nicht lokal gespeichert; ein genauer Vergleich ist nicht möglich.',
    logTimeGap:
      'Die Protokollerfassung hat eine zeitliche Lücke. Fehlende Zeiträume belegen keinen normalen Betrieb.',
    serviceRestarted:
      'Ein Dienstneustart hat die Beobachtung unterbrochen. Daraus folgt kein Anwendungsfehler.',
    resolvedSourceUnavailable:
      'Ein überprüfbarer vollständiger Quelltextcommit fehlt; ein genauer Quelltextvergleich ist nicht möglich.',
    sourceRunUnavailable:
      'Der zugehörige Quelltextlauf ist nicht verfügbar oder ungeprüft; sein Commit kann nicht bestätigt werden.',
    sourceRevisionConflict:
      'Die Quelltextcommit-Datensätze widersprechen sich und erlauben keinen genauen Quelltextvergleich.',
    sourceRevisionNotConfirmed:
      'Lauf- und Bereitstellungsdatensätze bestätigen den Quelltextcommit nicht übereinstimmend; ein Vergleich ist nicht verfügbar.',
    currentDeploymentIdentityUnavailable:
      'Der Identitätsdatensatz der aktuellen Bereitstellung ist nicht verfügbar; eine vertrauenswürdige Vergleichsbasis kann nicht geprüft werden.',
    deploymentHistoryUnavailable:
      'Die Bereitstellungshistorie ist nicht verfügbar; die Vergleichsbasis für Änderungen kann nicht geprüft werden.',
  },
  citationLabel: '{candidate} · {context} · Beleg {n}',
}
