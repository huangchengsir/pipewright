export default {
  title: 'OAuth-Apps',
  sectionDesc:
    'Registriere für jede git-Plattform eine OAuth-App (Client ID / Secret), damit Benutzer ihr Konto im Anmeldedaten-Tresor mit einem Klick „Verbinden“ und automatisch ein Token erhalten können, ohne manuell ein PAT einzufügen. Das Secret wird beim Speichern maskiert und kann nach dem Schreiben nicht mehr gelesen werden.',
  retry: 'Erneut versuchen',

  providerCustomLabel: 'Selbst gehostet',
  providerGiteeDesc: 'Gitee',
  providerCustomDesc: 'Selbst gehostetes GitLab / Gitea usw.',

  statusEnabled: 'Aktiviert',
  statusConfiguredIdle: 'Konfiguriert · Nicht aktiviert',
  statusUnconfigured: 'Nicht konfiguriert',

  clientIdPlaceholder: 'Auf der OAuth-App-Seite der git-Plattform abrufen',
  secretOptional: '(nach dem Schreiben wird nur die Maske angezeigt)',
  secretPlaceholderKeep: 'Leer lassen, um das vorhandene Secret beizubehalten',
  secretPlaceholderNew: 'Client Secret einfügen…',
  secretStored: 'Gespeichert: {masked}',

  toggleAria: '{provider} OAuth aktivieren',
  toggleTitle: 'Verbindung aktivieren',
  toggleDesc: 'Nach der Aktivierung erscheint die Schaltfläche „Verbinden“ dieser Plattform im Anmeldedaten-Tresor',

  lastSaved: 'Zuletzt gespeichert {time}',
  saveBtn: 'Speichern',

  errNoServer: 'Server nicht erreichbar. Bitte prüfe, ob das Backend läuft, und versuche es erneut',
  errVaultUnconfigured: 'Für den Tresor ist kein master key konfiguriert. Bitte setze die Umgebungsvariable PIPEWRIGHT_MASTER_KEY',
  errLoadStatus: 'Laden fehlgeschlagen ({status})',
  errLoadGeneric: 'OAuth-Apps konnten nicht geladen werden. Bitte versuche es später erneut',

  errClientIdRequiredEnabled: 'Bei Aktivierung ist die Client ID erforderlich',
  errSecretRequiredEnabled: 'Bei Aktivierung ist ein Client Secret erforderlich',
  errBaseUrlRequiredCustom: 'Eine selbst gehostete Instanz erfordert eine Base URL',
  errClientIdRequired: 'Bitte gib die Client ID ein',
  errBaseUrlRequired: 'Bitte gib die Base URL ein',

  toastSaveFailed: 'Speichern fehlgeschlagen',
  toastSaved: 'OAuth-App gespeichert',
  errNoServerShort: 'Server nicht erreichbar',
  unknownError: 'Unbekannter Fehler',

  justNow: 'gerade eben',
  minutesAgo: 'vor {n} Minuten',
  hoursAgo: 'vor {n} Stunden',
  daysAgo: 'vor {n} Tagen',
}
