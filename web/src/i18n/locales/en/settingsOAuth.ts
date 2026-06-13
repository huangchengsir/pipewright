export default {
  title: 'OAuth Apps',
  sectionDesc:
    'Register an OAuth app (Client ID / Secret) for each git platform so users can one-click "Connect" their account from the Credential Vault to automatically obtain a token, without manually pasting a PAT. The secret is masked on storage and cannot be read back after it is written.',
  retry: 'Retry',

  providerCustomLabel: 'Self-hosted',
  providerGiteeDesc: 'Gitee',
  providerCustomDesc: 'Self-hosted GitLab / Gitea, etc.',

  statusEnabled: 'Enabled',
  statusConfiguredIdle: 'Configured · Not enabled',
  statusUnconfigured: 'Not configured',

  clientIdPlaceholder: 'Get it from the OAuth app page of the git platform',
  secretOptional: '(only the mask is shown after writing)',
  secretPlaceholderKeep: 'Leave blank to keep the existing secret',
  secretPlaceholderNew: 'Paste Client Secret…',
  secretStored: 'Stored: {masked}',

  toggleAria: 'Enable {provider} OAuth',
  toggleTitle: 'Enable connection',
  toggleDesc: 'Once enabled, this platform\'s "Connect" button appears in the Credential Vault',

  lastSaved: 'Last saved {time}',
  saveBtn: 'Save',

  errNoServer: 'Unable to reach the server. Please check that the backend is running and retry',
  errVaultUnconfigured: 'The vault has no master key configured. Please set the PIPEWRIGHT_MASTER_KEY environment variable',
  errLoadStatus: 'Failed to load ({status})',
  errLoadGeneric: 'Failed to load OAuth apps. Please try again later',

  errClientIdRequiredEnabled: 'Client ID is required when enabled',
  errSecretRequiredEnabled: 'A Client Secret is required when enabled',
  errBaseUrlRequiredCustom: 'A self-hosted instance requires a Base URL',
  errClientIdRequired: 'Please enter the Client ID',
  errBaseUrlRequired: 'Please enter the Base URL',

  toastSaveFailed: 'Failed to save',
  toastSaved: 'OAuth app saved',
  errNoServerShort: 'Unable to reach the server',
  unknownError: 'Unknown error',

  justNow: 'just now',
  minutesAgo: '{n} minutes ago',
  hoursAgo: '{n} hours ago',
  daysAgo: '{n} days ago',
}
