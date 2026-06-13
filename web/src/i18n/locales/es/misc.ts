export default {
  // ─── shared ──────────────────────────────────────────────────────────────
  loadingShort: 'Cargando…',

  // ─── AuditTimeline ───────────────────────────────────────────────────────
  audit: {
    title: 'Registro de auditoría',
    sub: 'Quién · Cuándo · Sobre qué',
    treeAria: 'Cronología de auditoría',
    emptyLabel: 'Aún no hay registros de auditoría',
    emptyHint: 'Las operaciones sensibles como crear, modificar o eliminar credenciales y proyectos, restablecer secretos de webhook y disparar ejecuciones manualmente quedan registradas aquí. Los registros son inalterables.',
    loadMore: 'Cargar más registros de auditoría →',
    via: 'Consola web',
    actorYou: 'Tú',
    verbCreate: 'Creó',
    verbUpdate: 'Modificó',
    verbDelete: 'Eliminó',
    verbReset: 'Restableció',
    verbAdd: 'Conectó',
    verbTrigger: 'Disparó manualmente',
    verbDefault: 'Actuó sobre',
    nounCredential: 'credencial',
    nounWebhookSecret: 'secreto de firma de webhook',
    nounProject: 'proyecto',
    nounRun: 'ejecución',
    errConnect: 'No se puede conectar al servidor. Comprueba que el backend esté en ejecución e inténtalo de nuevo',
    errLoad: 'Error al cargar el registro de auditoría ({status})',
    errLoadRetry: 'Error al cargar el registro de auditoría. Inténtalo de nuevo más tarde',
  },

  // ─── CodeTree / CodeTreeNode ─────────────────────────────────────────────
  tree: {
    aria: 'Árbol de directorios de código',
    fileAria: 'Árbol de archivos del repositorio',
    title: 'Archivos',
    refTitle: 'Ref actual: {ref}',
    loadingDir: 'Cargando directorio…',
    emptyRepo: 'Repositorio vacío / código fuente no legible',
    emptyDir: 'Directorio vacío',
    errConnect: 'No se puede conectar al servidor',
    errNotFound: 'La ruta no existe',
    errLoad: 'Error al cargar ({status})',
    errLoadGeneric: 'Error al cargar',
  },

  // ─── CodeViewer ──────────────────────────────────────────────────────────
  code: {
    viewAria: 'Vista de código',
    editorAria: 'Editor de código (solo lectura)',
    noFileSelected: 'Ningún archivo seleccionado',
    truncated: 'Truncado',
    truncatedTitle: 'Archivo demasiado grande; solo se muestra la parte inicial',
    idleTitle: 'Selecciona un archivo a la izquierda para verlo',
    idleSub: 'Exploración de solo lectura del código fuente del repositorio con resaltado de sintaxis; no se puede editar ni confirmar.',
    binaryTitle: 'Archivo binario, no se puede previsualizar',
    degradedTitle: 'Código fuente no legible',
    degradedSub: 'Falló la clonación del repositorio o el entorno actual no puede acceder a él. Inténtalo más tarde o revisa la configuración del repositorio del proyecto.',
    errTitle: 'Error al cargar el archivo',
    fallbackRegionAria: 'Contenido del código (alternativa en texto plano)',
    fallbackNote: 'El componente de resaltado de sintaxis no se pudo cargar; se ha recurrido a una vista de texto plano.',
    errConnect: 'No se puede conectar al servidor',
    errNotFound: 'El archivo no existe',
    errLoad: 'Error al cargar ({status})',
    errLoadGeneric: 'Error al cargar',
  },

  // ─── ConfirmDialog ───────────────────────────────────────────────────────
  confirm: {
    cancel: 'Cancelar',
    confirm: 'Confirmar',
    typeLabelPrefix: 'Escribe',
    typeLabelSuffix: 'para confirmar',
    typePlaceholder: 'Escribe {text}…',
    typeAria: 'Escribe {text} para confirmar la acción',
  },

  // ─── EmptyState ──────────────────────────────────────────────────────────
  empty: {
    defaultTitle: 'Sin datos',
  },

  // ─── ErrorState ──────────────────────────────────────────────────────────
  error: {
    defaultTitle: 'Error al cargar',
    retry: 'Reintentar',
    aiUnavailableAria: 'Función de IA no disponible',
    aiTitle: 'Diagnóstico de fallos por IA',
    aiTag: 'No disponible',
    aiDesc: 'El proveedor de LLM no respondió, por lo que esta vez no se generó ningún diagnóstico. Los resultados de las ejecuciones y los registros se guardan con normalidad; el CI/CD principal no se ve afectado.',
    confidenceLabel: 'Confianza {n}% · {level}',
    confidenceHigh: 'Alta',
    confidenceMedium: 'Media',
    confidenceLow: 'Baja',
  },

  // ─── ToastHost ───────────────────────────────────────────────────────────
  toast: {
    hostAria: 'Notificaciones',
    itemAria: 'Notificación {type}: {title}',
    closeAria: 'Cerrar notificación: {title}',
  },
}
