export default {
  title: 'Aplicaciones OAuth',
  sectionDesc:
    'Registra una aplicación OAuth (Client ID / Secret) para cada plataforma git para que los usuarios puedan «Conectar» su cuenta con un clic desde la bóveda de credenciales y obtener un token automáticamente, sin pegar manualmente un PAT. El secreto se enmascara al guardarlo y no puede leerse de nuevo tras escribirlo.',
  retry: 'Reintentar',

  providerCustomLabel: 'Autoalojado',
  providerGiteeDesc: 'Gitee',
  providerCustomDesc: 'GitLab / Gitea autoalojado, etc.',

  statusEnabled: 'Activado',
  statusConfiguredIdle: 'Configurado · No activado',
  statusUnconfigured: 'Sin configurar',

  clientIdPlaceholder: 'Obténlo en la página de la aplicación OAuth de la plataforma git',
  secretOptional: '(tras escribirlo solo se muestra la máscara)',
  secretPlaceholderKeep: 'Déjalo en blanco para conservar el secreto existente',
  secretPlaceholderNew: 'Pega el Client Secret…',
  secretStored: 'Guardado: {masked}',

  toggleAria: 'Activar OAuth de {provider}',
  toggleTitle: 'Activar conexión',
  toggleDesc: 'Una vez activado, el botón «Conectar» de esta plataforma aparece en la bóveda de credenciales',

  lastSaved: 'Último guardado {time}',
  saveBtn: 'Guardar',

  errNoServer: 'No se puede conectar al servidor. Comprueba que el backend esté en ejecución y reintenta',
  errVaultUnconfigured: 'La bóveda no tiene una master key configurada. Establece la variable de entorno PIPEWRIGHT_MASTER_KEY',
  errLoadStatus: 'Error al cargar ({status})',
  errLoadGeneric: 'No se pudieron cargar las aplicaciones OAuth. Inténtalo de nuevo más tarde',

  errClientIdRequiredEnabled: 'El Client ID es obligatorio al activar',
  errSecretRequiredEnabled: 'Se requiere un Client Secret al activar',
  errBaseUrlRequiredCustom: 'Una instancia autoalojada requiere una Base URL',
  errClientIdRequired: 'Introduce el Client ID',
  errBaseUrlRequired: 'Introduce la Base URL',

  toastSaveFailed: 'Error al guardar',
  toastSaved: 'Aplicación OAuth guardada',
  errNoServerShort: 'No se puede conectar al servidor',
  unknownError: 'Error desconocido',

  justNow: 'ahora mismo',
  minutesAgo: 'hace {n} minutos',
  hoursAgo: 'hace {n} horas',
  daysAgo: 'hace {n} días',
}
