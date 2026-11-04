export default {
  groups: {
    resources: "Recursos y despliegues",
    integrations: "Integraciones e IA",
    security: "Seguridad y cuenta",
    system: "Sistema y notificaciones",
  },
  close: "Cerrar",
  cancel: "Cancelar",
  confirm: "Confirmar y continuar",
  save: "Guardar",
  edit: "Editar",
  delete: "Eliminar",
  remove: "Quitar",
  name: "Nombre",
  select: "Seleccionar…",
  none: "Ninguna",
  loading: "Cargando…",
  enabled: "Activado",
  disabled: "Desactivado",
  enable: "Activar",
  disable: "Desactivar",
  credentials: "Credenciales de Kubernetes",
  addCredential: "Añadir credencial Kubernetes",
  rotate: "Renovar credenciales",
  authentication: "Autenticación",
  serverToken: "Servidor y token",
  context: "Contexto",
  server: "URL del servidor API",
  ca: "Certificado CA (PEM)",
  token: "Token de acceso",
  tlsName: "Nombre TLS del servidor (opcional)",
  preview: "Comprobar formato",
  credential: "Credencial",
  identity: "Detalles técnicos",
  addConnection: "Añadir conexión",
  editConnection: "Editar conexión",
  emptyConnections: "Conectar un clúster",
  manageCredentials: "Gestionar credenciales del clúster",
  grants: "Destinos autorizados",
  addGrant: "Añadir permiso",
  grantTitle: "Configurar permiso de destino",
  project: "Proyecto",
  environment: "Entorno",
  namespace: "Espacio de nombres",
  deployment: "Deployment",
  container: "Contenedor",
  containers: "Contenedores permitidos",
  executor: "Ejecutor",
  executors: "Ejecutores permitidos",
  local: "Docker local",
  sshServer: "Servidor SSH",
  addSSH: "Añadir ejecutor SSH",
  hostKey: "Huella del host SSH",
  profile: "Perfil de herramienta",
  toolImage: "Imagen de herramienta (digest fijo)",
  architecture: "Arquitectura",
  kubectlPath: "Ruta de kubectl",
  registryCredential: "Credencial del registro (opcional)",
  approvalRequired: "Requiere aprobación",
  approvalOptional: "Usar política de aprobación del pipeline",
  probe: "Comprobar conexión",
  replicas: "Réplicas listas",
  target: "Destino del despliegue",
  connection: "Conexión del clúster",
  manageConnections: "Gestionar conexiones y permisos",
  jobTitle: "Despliegue Kubernetes",
  imageSource: "Origen de imagen",
  more: "Más opciones",
  timeout: "Espera de disponibilidad (segundos)",
  passwordTitle: "Confirme su identidad",
  password: "Contraseña actual",
  unsaved: "¿Descartar cambios sin guardar?",
  discard: "Descartar cambios",
  receipt: "Resultado del despliegue",
  reconcile: "Comprobar estado del clúster",
  retire: "Finalizar seguimiento local",
  retired: "Seguimiento finalizado",
  operation: "ID de operación",
  sourceRun: "Ejecución de origen",
  intent: "Hash de intención de despliegue",
  revision: "Revisión",
  reason: "Código de diagnóstico",
  phases: "Pasos del despliegue",
  recorded: "Registrado",
  notRecorded: "Sin registro",
  credentialsHint: "Autenticación del clúster separada de Git y SSH.",
  emptyCredentials: "No hay credenciales Kubernetes configuradas.",
  secretHint:
    "Los secretos se borran al guardar o cancelar. Para renovarlos introduzca todos los datos; no se permite verlos en texto claro.",
  previewReady: "Formato válido; conexión al clúster sin comprobar",
  connectionsHint:
    "Despliegue en Deployments existentes mediante conexiones fiables y permisos explícitos.",
  setupHint:
    "Añada credenciales al almacén y configure la imagen de herramienta y los destinos permitidos.",
  grantHint:
    "Permita solo este proyecto, entorno, carga, contenedores y ejecutor.",
  emptyGrants:
    "No hay destinos autorizados. Añada un permiso antes de comprobar o desplegar.",
  containersHint: "Separe los nombres con comas. No se admiten comodines.",
  hostKeyHint:
    "Verifique la huella SHA256 por un canal fiable. Se exige coincidencia exacta.",
  profileHint:
    "Guardar no valida la herramienta. Compruebe la conexión para validar su entorno.",
  probeHint:
    "Solo lee el destino permitido elegido. No actualiza imágenes ni despliega.",
  probeReady: "Conexión verificada; destino leído",
  jobDescription:
    "Actualice la imagen de un Deployment existente y espere a que esté listo.",
  separateStage:
    "Se necesita una etapa exclusiva sin matrices, nodos paralelos ni tareas extra. Use Añadir nodo.",
  noProjectGrant: "No hay permiso para este proyecto. Añádalo en ajustes.",
  staleTarget: "Destino anterior no disponible; revise los permisos",
  unavailableChoice: "Selección anterior no disponible",
  imageSourceHint:
    "Elija un nodo previo de publicación. Se usará su digest real del registro.",
  noPushSource:
    "No hay nodo previo de publicación. Añádalo y conéctelo primero.",
  passwordHint:
    "Esta acción requiere verificar la contraseña de la cuenta en los últimos 5 minutos.",
  unsavedBody: "Al salir se borrará el formulario actual.",
  deleteBody: "¿Eliminar «{name}»? No se pueden eliminar objetos en uso.",
  noReceipt:
    "No hay resultado único de este nodo. No se confirma éxito ni posibilidad de reintento.",
  retireBody:
    "Finaliza el seguimiento para permitir un despliegue completo nuevo. No revierte el clúster ni marca la ejecución original como exitosa.",
  unknownHint:
    "El despliegue podría haberse aplicado. Compruebe antes de volver a enviarlo.",
  cleanupHint:
    "La limpieza del contenedor no está confirmada; reintento bloqueado.",
  historyHint:
    "La comprobación no modifica la ejecución original. La siguiente deberá validar el destino de nuevo.",
  notRecordedHint:
    "Sin evidencia de este paso; no se confirma que haya comenzado o pasado.",
  noRollback:
    "Este historial incluye Kubernetes; no permite reversión SSH. Despliegue la versión deseada con una ejecución completa nueva.",
  adopted: "Despliegue de origen revalidado y reutilizado",
  rolloutOnly:
    "Solo se verificó la disponibilidad del despliegue, no su supervisión continua.",
  phase: {
    preflight: "Comprobación previa",
    update: "Actualizar imagen",
    rollout: "Esperar disponibilidad",
    cleanup: "Limpiar herramientas",
  },
  phaseCode: {
    patch_not_authorized:
      "Estas credenciales del clúster no permiten actualizar este Deployment. Pida a un administrador que revise los permisos. No se envió ninguna actualización.",
    permission_check_unverified:
      "No se pudieron verificar los permisos de despliegue en el clúster. Revise la conexión y la configuración de comprobación de permisos. No se envió ninguna actualización.",
    preflight_started: "Comprobando permisos, herramienta y destino.",
    image_update_dispatched:
      "Actualización enviada; el resultado se determina por la confirmación.",
    rollout_observing: "Observando la disponibilidad de réplicas.",
    rollout_ready: "Esta observación confirmó que la carga está lista.",
    cleanup_started:
      "Limpiando el contenedor de herramienta de esta operación.",
    cleanup_verified: "Limpieza de herramienta confirmada.",
    cleanup_unverified: "Limpieza sin confirmar; revise el resultado.",
  },
  business: {
    not_dispatched: "Preparando despliegue",
    submitted: "Enviado, esperando disponibilidad",
    ready: "Carga lista",
    unchanged: "Versión ya aplicada, disponibilidad verificada",
    failed: "Despliegue incompleto",
    superseded: "Destino sustituido por otro cambio",
    outcome_unknown: "Resultado del despliegue sin confirmar",
  },
  cleanup: {
    pending: "Limpieza pendiente",
    verified: "Limpieza confirmada",
    blocked: "Limpieza bloqueada",
  },
  errors: {
    invalid: "Configuración no válida. Revise los campos.",
    session: "Sesión caducada. Vuelva a iniciar sesión.",
    passwordRequired: "Verifique de nuevo su contraseña antes de esta acción.",
    passwordInvalid:
      "Falló la verificación de contraseña. Revísela e inténtelo de nuevo.",
    denied: "Conexión o destino no autorizado.",
    missing: "Registro no encontrado. Actualice la lista.",
    conflict:
      "Configuración modificada. Borrador conservado; actualice y revise antes de guardar.",
    inUse: "Este registro aún está referenciado y no puede modificarse.",
    held: "Un despliegue sin terminar ocupa este destino. Compruebe la operación original.",
    ownerUnknown:
      "Salida del ejecutor original sin confirmar. Relevo y reintento bloqueados.",
    runtime:
      "Fallaron las comprobaciones de seguridad. Revise herramienta y ejecutor.",
    credentialUnavailable:
      "Credenciales del clúster no disponibles. Revise almacén y conexión.",
    observation:
      "Observación interrumpida; resultado sin confirmar. Revise la operación original.",
    unavailable: "Servicio de ejecución no disponible. Revise la conexión.",
    uncertain:
      "Resultado incierto; reenvío bloqueado. Actualice los registros. Un nombre coincidente no confirma la creación.",
    load: "No se pudieron cargar los datos. Actualice para reintentar.",
  },
  readBack: "Consultar registros",
  acknowledge: "Revisado; finalizar edición",
  readBackHint:
    "Registros actualizados. Revise identidad y revisión; el resultado original no se determina automáticamente.",
  targetCount: "{n} destinos de despliegue",
};
