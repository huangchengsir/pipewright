export default {
  title: 'AI-Anbieter',
  subtitle:
    'Pipewright trainiert keine eigenen Modelle – binde dein eigenes LLM für die Fehlerdiagnose und Konfigurationserstellung ein. Schlüssel liegen ausschließlich im verschlüsselten Tresor dieser Instanz und verlassen ihn nie.',
  statusConfigured: 'Konfiguriert',
  statusUnconfigured: 'Nicht konfiguriert',
  retry: 'Erneut versuchen',

  providerClaudeTag: 'Empfohlen für Diagnose',
  providerOllamaDesc: 'Lokal / selbst gehostet',
  providerOllamaTag: 'Kein Datenabfluss',

  guidanceAria: 'AI-Konfigurationsleitfaden',
  guidanceTitle: 'Konfiguriere ein LLM, um die AI-Diagnose freizuschalten',
  guidanceBody:
    'Sobald du Claude, OpenAI oder ein lokales Ollama verbindest, generiert Pipewright bei einem Pipeline-Fehler automatisch Ursachenhypothesen und Korrekturvorschläge – ohne manuelles Durchsuchen der Logs.',

  selectProvider: 'Anbieter auswählen',
  providerRadioAria: 'AI-Anbieterauswahl',
  selectProviderAria: '{name} auswählen',
  providerConfig: '{name}-Konfiguration',
  lastSaved: 'Zuletzt gespeichert {time}',

  apiKeyHint: 'Nach dem Speichern wird nur ein maskierter Wert angezeigt; leer lassen, um den vorhandenen Schlüssel zu behalten',
  apiKeyReplacing: 'Wird ersetzt…',
  apiKeyConfigured: 'Konfiguriert •••• (leer lassen = unverändert)',
  apiKeyPaste: 'API Key einfügen…',
  apiKeyMaskedAria: 'Konfigurierte Maske: {masked}',

  ollamaHint: 'Lokales Ollama benötigt keinen API Key – stelle nur sicher, dass der Ollama-Dienst unter der angegebenen Adresse läuft.',

  baseUrlLabel: 'Base URL',
  baseUrlHint: 'Standard: {url}',

  modelLabel: 'Modell',
  modelHint: 'Hauptmodell für die Diagnose, z. B. claude-opus-4-7 / gpt-4o / llama3',

  testConnection: 'Verbindung testen',
  testOk: 'Verbindung OK · Latenz {ms}ms',
  testFail: 'Verbindung fehlgeschlagen',

  budgetLabel: 'Monatliches Token-Limit',
  budgetHint: 'Pausiert die AI-Diagnose bei Überschreitung (leer = unbegrenzt; in diesem Zyklus nur deklariert, im nächsten Epic erzwungen)',
  budgetPlaceholder: 'z. B. 500000, leer = unbegrenzt',

  enableAi: 'AI-Funktionen aktivieren',
  enableAiDesc: 'Im ausgeschalteten Zustand wird die AI-Diagnose still übersprungen und die zentralen CI/CD-Pipelines bleiben unberührt',

  dirtyNote: 'Es gibt ungespeicherte Änderungen',
  cleanNote: 'Keine Änderungen',
  discard: 'Verwerfen',
  saveChanges: 'Änderungen speichern',

  toastSaveSuccess: 'AI-Einstellungen gespeichert',
  toastSaveFailed: 'Speichern fehlgeschlagen',

  errServerUnreachable: 'Server nicht erreichbar. Prüfe, ob das Backend läuft, und versuche es erneut.',
  errServerUnreachableShort: 'Server nicht erreichbar',
  errVaultUnconfigured: 'Im Tresor ist kein Master Key konfiguriert. Setze die Umgebungsvariable PIPEWRIGHT_MASTER_KEY.',
  errLoadFailed: 'Laden fehlgeschlagen ({status})',
  errLoadGeneric: 'AI-Einstellungen konnten nicht geladen werden. Bitte später erneut versuchen.',
  errBudgetInvalid: 'Das monatliche Token-Limit muss eine positive Ganzzahl oder leer sein',
  errProviderInvalid: 'Bitte einen gültigen Anbieter auswählen',
  errBaseUrlRequired: 'Bitte die Base URL eingeben',
  errApiKeyRequired: 'API Key darf nicht leer sein (außer bei Ollama erforderlich)',
  errRequestFailed: 'Anfrage fehlgeschlagen ({status})',
  errUnknown: 'Unbekannter Fehler',
}
