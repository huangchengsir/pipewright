export default {
  title: 'AI Provider',
  subtitle:
    'Pipewright trains no models of its own — bring your own LLM for failure diagnosis and config generation. Keys live only in this instance’s encrypted vault and never leave it.',
  statusConfigured: 'Configured',
  statusUnconfigured: 'Not configured',
  retry: 'Retry',

  providerClaudeTag: 'Recommended for diagnosis',
  providerOllamaDesc: 'Local / self-hosted',
  providerOllamaTag: 'Zero egress',

  guidanceAria: 'AI configuration guide',
  guidanceTitle: 'Configure an LLM to unlock AI diagnosis',
  guidanceBody:
    'Once you connect Claude, OpenAI, or a local Ollama, Pipewright automatically generates root-cause hypotheses and fix suggestions when a pipeline fails — no manual log digging required.',

  selectProvider: 'Select a provider',
  providerRadioAria: 'AI provider selection',
  selectProviderAria: 'Select {name}',
  providerConfig: '{name} configuration',
  lastSaved: 'Last saved {time}',

  apiKeyHint: 'Only a masked value is shown after writing; leave blank to keep the existing key',
  apiKeyReplacing: 'Replacing…',
  apiKeyConfigured: 'Configured •••• (leave blank to keep)',
  apiKeyPaste: 'Paste API Key…',
  apiKeyMaskedAria: 'Configured mask: {masked}',

  ollamaHint: 'Local Ollama needs no API Key — just make sure the Ollama service is running at the given address.',

  baseUrlLabel: 'Base URL',
  baseUrlHint: 'Default: {url}',

  modelLabel: 'Model',
  modelHint: 'Primary model used for diagnosis, e.g. claude-opus-4-7 / gpt-4o / llama3',

  testConnection: 'Test connection',
  testOk: 'Connection OK · latency {ms}ms',
  testFail: 'Connection failed',

  budgetLabel: 'Monthly Token Limit',
  budgetHint: 'Pauses AI diagnosis once exceeded (blank = unlimited; declared this cycle, enforced in the next Epic)',
  budgetPlaceholder: 'e.g. 500000, blank = unlimited',

  enableAi: 'Enable AI features',
  enableAiDesc: 'When off, AI diagnosis is silently skipped and core CI/CD pipelines are unaffected',

  dirtyNote: 'You have unsaved changes',
  cleanNote: 'No changes',
  discard: 'Discard',
  saveChanges: 'Save changes',

  toastSaveSuccess: 'AI settings saved',
  toastSaveFailed: 'Save failed',

  errServerUnreachable: 'Cannot reach the server. Check that the backend is running and retry.',
  errServerUnreachableShort: 'Cannot reach the server',
  errVaultUnconfigured: 'Vault has no master key configured. Set the PIPEWRIGHT_MASTER_KEY environment variable.',
  errLoadFailed: 'Load failed ({status})',
  errLoadGeneric: 'Failed to load AI settings. Please try again later.',
  errBudgetInvalid: 'Monthly token limit must be a positive integer or left blank',
  errProviderInvalid: 'Please select a valid provider',
  errBaseUrlRequired: 'Please enter the base URL',
  errApiKeyRequired: 'API Key cannot be empty (required for non-Ollama)',
  errRequestFailed: 'Request failed ({status})',
  errUnknown: 'Unknown error',
}
