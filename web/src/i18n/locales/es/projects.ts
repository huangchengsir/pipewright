export default {
  // ─── page header ───────────────────────────────────────────────
  title: 'Proyectos',
  subtitle: 'Repositorios de Gitee gestionados: cada proyecto corresponde a una configuración de pipeline y destinos de despliegue',
  newProject: 'Nuevo proyecto',
  retry: 'Reintentar',

  // ─── toolbar / search / filter ─────────────────────────────────
  searchFilterAria: 'Buscar y filtrar proyectos',
  searchPlaceholder: 'Buscar por nombre, URL del repositorio o rama…',
  searchAria: 'Buscar proyectos',
  statusFilterAria: 'Filtrar por estado',
  statusAll: 'Todos los estados',

  // ─── list states ───────────────────────────────────────────────
  loading: 'Cargando',
  emptyTitle: 'Aún no hay proyectos',
  emptyHint: 'Conecta tu primer repositorio de Gitee y luego configura un pipeline y despliega en servidores de destino.',
  noMatchTitle: 'No hay proyectos coincidentes',
  noMatchHint: 'Ajusta el término de búsqueda o el filtro de estado e inténtalo de nuevo.',
  clearFilter: 'Limpiar filtros',
  resultCount: '{n} proyectos',
  resultCountTotal: '(de {total})',

  // ─── project card ──────────────────────────────────────────────
  runStatusAria: 'Estado de la ejecución: {status}',
  lastRun: 'Última ejecución',
  noRun: 'Sin ejecuciones',
  targetServers: 'Servidores de destino',
  notBound: 'Sin vincular',
  credential: 'Credencial del repositorio',
  credentialRefTitle: 'Referencia de credencial (no texto plano)',
  updatedAt: 'Actualizado {time}',

  // ─── card actions ──────────────────────────────────────────────
  actionRunTitle: 'Disparar ejecución manualmente · {name}',
  actionRunAria: 'Disparar manualmente una ejecución de pipeline del proyecto {name}',
  actionRenameTitle: 'Renombrar {name}',
  actionRenameAria: 'Renombrar el proyecto {name}',
  actionCodeTitle: 'Explorar código · {name}',
  actionCodeAria: 'Explorar el código del proyecto {name}',
  actionPipelineTitle: 'Configuración del pipeline · {name}',
  actionPipelineAria: 'Configurar el pipeline del proyecto {name}',
  actionDeleteTitle: 'Eliminar {name}',
  actionDeleteAria: 'Eliminar el proyecto {name}',

  // ─── shared modal ──────────────────────────────────────────────
  closeDialog: 'Cerrar diálogo',
  cancel: 'Cancelar',
  save: 'Guardar',
  saving: 'Guardando…',

  // ─── trigger modal ─────────────────────────────────────────────
  triggerDialogAria: 'Disparo manual · {name}',
  triggerTitle: 'Disparar ejecución manualmente',
  triggerSub: '{name} · elige una rama y crea una ejecución de pipeline al instante',
  branch: 'Rama',
  branchHint: '(opcional, déjalo vacío para usar la rama predeterminada del proyecto)',
  commit: 'Commit',
  commitHint: '(opcional, déjalo vacío para usar el HEAD de la rama)',
  commitPlaceholder: 'p. ej. a3f1c2d',
  params: 'Parámetros',
  paramsHintTyped: '(rellena según la definición; se inyectan en el pipeline como variables de entorno)',
  paramsHintFree: '(opcional, se inyectan en el pipeline como variables de entorno)',
  triggering: 'Disparando…',
  runNow: 'Ejecutar ahora',

  // ─── create modal ──────────────────────────────────────────────
  createSub: 'Conecta un repositorio de Gitee y vincula una credencial del repositorio',
  fieldName: 'Nombre del proyecto',
  fieldNamePlaceholder: 'p. ej. acme-web',
  fieldRepo: 'URL del repositorio',
  fieldCredHint: '(solo se muestran credenciales de token de Git, nunca texto plano)',
  credLoading: 'Cargando credenciales…',
  credSelect: 'Selecciona una credencial de token de Git',
  credEmptyPre: 'Aún no hay credenciales de token de Git. Ve a la',
  credVaultLink: 'bóveda de credenciales',
  credEmptyPost: 'para añadir una.',
  fieldDefaultBranch: 'Rama predeterminada',
  fieldDefaultBranchHint: '(opcional, déjalo vacío para detectarla automáticamente al probar la conexión)',
  testConnection: 'Probar conexión',
  testing: 'Probando…',
  testOk: 'Conexión exitosa',
  testDetectedBranch: '· rama predeterminada {branch}',
  creating: 'Creando…',
  createSubmit: 'Crear proyecto',

  // ─── rename modal ──────────────────────────────────────────────
  renameTitle: 'Renombrar proyecto',
  renameSub: 'Cambia el nombre visible del proyecto',

  // ─── delete modal ──────────────────────────────────────────────
  deleteDialogAria: 'Confirmar eliminación del proyecto',
  deleteTitle: 'Eliminar proyecto',
  deleteSub: 'Esta acción no se puede deshacer',
  deleteConfirmPre: '¿Seguro que quieres eliminar permanentemente el proyecto',
  deleteConfirmPost: '? Se limpiarán su configuración de pipeline, su historial de ejecuciones y las referencias de credenciales.',
  deleting: 'Eliminando…',
  confirmDelete: 'Confirmar eliminación',

  // ─── error messages ────────────────────────────────────────────
  errNetwork: 'No se puede conectar con el servidor. Comprueba que el backend esté en ejecución y reinténtalo.',
  errNetworkRetry: 'No se puede conectar con el servidor. Inténtalo de nuevo más tarde.',
  errLoadNetwork: 'No se puede conectar con el servidor. Comprueba que el backend esté en ejecución y reinténtalo',
  errLoadStatus: 'Error al cargar los proyectos ({status})',
  errLoadRetry: 'Error al cargar los proyectos, inténtalo de nuevo más tarde',
  errNameRequired: 'Introduce un nombre de proyecto',
  errRepoRequired: 'Introduce una URL del repositorio',
  errRepoFormat: 'Formato de URL del repositorio no válido. Debe empezar por https:// o git{\'@\'}',
  errCredRequired: 'Selecciona una credencial del repositorio',
  errRepoFirst: 'Introduce primero una URL del repositorio',
  errCredFirst: 'Selecciona primero una credencial del repositorio',
  errNameEmpty: 'El nombre del proyecto no puede estar vacío',

  testErrCredential: 'Error de credencial: comprueba que tu token de acceso de Gitee sea válido y actualízalo en la bóveda de credenciales.',
  testErrUnreachable: 'Repositorio inaccesible: confirma que la URL sea correcta y que el repositorio exista y sea accesible.',
  testErrVault: 'La bóveda no tiene configurada una master key, por lo que no se pueden leer las credenciales.',
  testErrStatus: 'La prueba de conexión falló ({status})',
  testErrRetry: 'La prueba de conexión falló, inténtalo de nuevo más tarde.',

  createErrCredField: 'Error de credencial: comprueba que el token de acceso sea válido',
  createErrCredBanner: 'La validación de la credencial falló. Cambia de credencial o actualízala en la bóveda de credenciales.',
  createErrRepoField: 'Repositorio inaccesible: confirma que la URL sea correcta y accesible',
  createErrRepoBanner: 'La URL del repositorio es inaccesible; la creación falló.',
  createErrVault: 'La bóveda no tiene configurada una master key, por lo que no se puede guardar el proyecto.',
  createErrStatus: 'La creación falló ({status})',
  createErrRetry: 'La creación falló, inténtalo de nuevo más tarde.',

  renameErrStatus: 'El cambio de nombre falló ({status})',
  renameErrRetry: 'El cambio de nombre falló, inténtalo de nuevo más tarde.',

  deleteErrStatus: 'La eliminación falló ({status})',
  deleteErrRetry: 'La eliminación falló, inténtalo de nuevo más tarde.',

  triggerErrNotFound: 'El proyecto no existe; actualiza y reinténtalo.',
  triggerErrStatus: 'El disparo falló ({status})',
  triggerErrRetry: 'El disparo falló, inténtalo de nuevo más tarde.',
}
