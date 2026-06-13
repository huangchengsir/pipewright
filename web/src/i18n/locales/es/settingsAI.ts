export default {
  title: 'Proveedor de AI',
  subtitle:
    'Pipewright no entrena modelos propios: conecta tu propio LLM para el diagnóstico de fallos y la generación de configuración. Las claves se guardan solo en la bóveda cifrada de esta instancia y nunca salen de ella.',
  statusConfigured: 'Configurado',
  statusUnconfigured: 'Sin configurar',
  retry: 'Reintentar',

  providerClaudeTag: 'Recomendado para diagnóstico',
  providerOllamaDesc: 'Local / autoalojado',
  providerOllamaTag: 'Sin salida de datos',

  guidanceAria: 'Guía de configuración de AI',
  guidanceTitle: 'Configura un LLM para desbloquear el diagnóstico con AI',
  guidanceBody:
    'Una vez que conectes Claude, OpenAI o un Ollama local, Pipewright generará automáticamente hipótesis de causa raíz y sugerencias de corrección cuando falle un pipeline, sin necesidad de revisar registros manualmente.',

  selectProvider: 'Selecciona un proveedor',
  providerRadioAria: 'Selección de proveedor de AI',
  selectProviderAria: 'Seleccionar {name}',
  providerConfig: 'Configuración de {name}',
  lastSaved: 'Último guardado {time}',

  apiKeyHint: 'Tras guardarla solo se muestra un valor enmascarado; déjala en blanco para conservar la clave existente',
  apiKeyReplacing: 'Reemplazando…',
  apiKeyConfigured: 'Configurada •••• (en blanco para no cambiar)',
  apiKeyPaste: 'Pega la API Key…',
  apiKeyMaskedAria: 'Máscara configurada: {masked}',

  ollamaHint: 'Ollama local no necesita API Key: solo asegúrate de que el servicio de Ollama esté en ejecución en la dirección indicada.',

  baseUrlLabel: 'Base URL',
  baseUrlHint: 'Predeterminado: {url}',

  modelLabel: 'Modelo',
  modelHint: 'Modelo principal usado para el diagnóstico, p. ej. claude-opus-4-7 / gpt-4o / llama3',

  testConnection: 'Probar conexión',
  testOk: 'Conexión correcta · latencia {ms}ms',
  testFail: 'Error de conexión',

  budgetLabel: 'Límite mensual de Token',
  budgetHint: 'Pausa el diagnóstico con AI al superarlo (en blanco = sin límite; declarado en este ciclo, aplicado en el próximo Epic)',
  budgetPlaceholder: 'p. ej. 500000, en blanco = sin límite',

  enableAi: 'Activar funciones de AI',
  enableAiDesc: 'Cuando está desactivado, el diagnóstico con AI se omite silenciosamente y los pipelines de CI/CD principales no se ven afectados',

  dirtyNote: 'Tienes cambios sin guardar',
  cleanNote: 'Sin cambios',
  discard: 'Descartar',
  saveChanges: 'Guardar cambios',

  toastSaveSuccess: 'Configuración de AI guardada',
  toastSaveFailed: 'Error al guardar',

  errServerUnreachable: 'No se puede conectar con el servidor. Comprueba que el backend esté en ejecución y reintenta.',
  errServerUnreachableShort: 'No se puede conectar con el servidor',
  errVaultUnconfigured: 'La bóveda no tiene una master key configurada. Define la variable de entorno PIPEWRIGHT_MASTER_KEY.',
  errLoadFailed: 'Error al cargar ({status})',
  errLoadGeneric: 'No se pudo cargar la configuración de AI. Inténtalo de nuevo más tarde.',
  errBudgetInvalid: 'El límite mensual de tokens debe ser un entero positivo o quedar en blanco',
  errProviderInvalid: 'Selecciona un proveedor válido',
  errBaseUrlRequired: 'Introduce la base URL',
  errApiKeyRequired: 'La API Key no puede estar vacía (obligatoria salvo en Ollama)',
  errRequestFailed: 'La solicitud falló ({status})',
  errUnknown: 'Error desconocido',
}
