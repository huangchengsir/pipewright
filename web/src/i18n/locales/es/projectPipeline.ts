export default {
  // ─── barra superior / migas de pan ──────────────────────────────
  breadcrumbAria: 'Navegación de migas de pan',
  breadcrumbProjects: 'Proyectos',
  title: 'Configuración del pipeline',

  // ─── pestañas ───────────────────────────────────────────────────
  tabCanvas: 'Lienzo del pipeline',
  tabVars: 'Variables y caché',
  tabTriggers: 'Ajustes de activación',
  tabEnvs: 'Entornos y credenciales',
  tabStripAria: 'Pestañas de configuración del pipeline',

  // ─── Pipeline como código (GitOps · FR-8-12) ────────────────────
  pacTitle: 'Pipeline como código',
  pacOnHint: 'Las ejecuciones leen .pipewright.yml desde la rama de ejecución en el repositorio (con reserva a esta configuración si falta o no es válido).',
  pacOffHint: 'Cuando está activo, cada ejecución lee .pipewright.yml desde la raíz del repositorio en la rama de ejecución (con reserva a esta configuración si falta o no es válido).',
  pacToggleFailed: 'No se pudo cambiar. Inténtalo de nuevo.',

  // ─── botones de la barra de herramientas ────────────────────────
  aiGenerate: 'Generar pipeline con IA',
  importYaml: 'Importar desde YAML',
  templates: 'Plantillas',
  validate: 'Validar configuración',
  closeValidationPanel: 'Cerrar panel de validación',
  badgeReady: 'Listo',
  badgeErrors: '{n} errores',
  saving: 'Guardando…',
  saveDraft: 'Guardar borrador',

  // ─── banners / estado ───────────────────────────────────────────
  dismiss: 'Descartar',
  draftSaved: 'Borrador del pipeline guardado',
  retry: 'Reintentar',
  loading: 'Cargando',

  // ─── errores de carga ───────────────────────────────────────────
  errNoServer: 'No se puede conectar con el servidor. Comprueba que el backend esté en ejecución e inténtalo de nuevo.',
  errProjectNotFound: 'El proyecto no existe. Verifica que el ID del proyecto sea correcto.',
  errLoadFailedStatus: 'Error al cargar el pipeline ({status})',
  errLoadFailedRetry: 'Error al cargar el pipeline. Inténtalo de nuevo más tarde.',

  // ─── errores de guardado ────────────────────────────────────────
  errSaveFailedRetry: 'Error al guardar. Inténtalo de nuevo más tarde.',
  errSaveFailedStatus: 'Error al guardar ({status})',
  errInvalidStage: 'El nombre de la etapa no puede estar vacío y kind debe ser un valor permitido. Revísalo e inténtalo de nuevo.',
  errInvalidJob: 'El nombre o el tipo de la tarea no pueden estar vacíos. Complétalos e inténtalo de nuevo.',
  errDuplicateId: 'ID de etapa o tarea duplicado. Elimina los duplicados e inténtalo de nuevo.',
  errInvalidBuild: 'El modelo de compilación debe ser dockerfile/toolchain y el tipo de artefacto debe ser image/jar/dist.',
  errInvalidVar: 'La clave de la variable no puede estar vacía y debe ser única dentro de su ámbito; las variables secret requieren una credencial del vault.',
  errInvalidEnvironment: 'El nombre del entorno no puede estar vacío y el tipo de registro de imágenes debe ser harbor/acr/dockerhub/custom.',
  errCredentialNotFound: 'La credencial del vault referenciada no existe. Vuelve a seleccionarla e inténtalo de nuevo.',
  errVaultUnconfigured: 'El vault no tiene configurada una master key, por lo que no se pueden referenciar credenciales secret.',
}
