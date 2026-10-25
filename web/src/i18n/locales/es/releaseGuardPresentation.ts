export default {
  aiTitle: 'Interpretación con IA',
  boundaryDetails: 'Límites del informe y conservación de pruebas',
  titles: {
    notFound: 'Ausencia del contenedor confirmada al muestrear',
    unknown: 'Esta prueba aún no puede interpretarse',
    unavailable: 'No se obtuvo una muestra válida',
    running: 'Contenedor en ejecución al muestrear',
    exited: 'Contenedor detenido al muestrear',
    oom: 'Terminación por falta de memoria registrada',
    image: 'Identidad de imagen esperada registrada',
    gateFailed: 'Falló el control de salud del despliegue',
    logs: 'Registros limitados del período conservados',
    unhealthy: 'La comprobación de salud Docker falló al muestrear',
    logsUnavailable: 'No se obtuvieron registros del período',
    logsPartial: 'Solo se conservan registros parciales',
    logsEmpty: 'Período de registros obtenido; sin líneas devueltas',
  },
  logExcerpt:
    'Extracto de registros (hasta 600 caracteres; contenido completo en detalles técnicos)',
  snapshotScope:
    'Interpreta solo la instantánea elegida y autorizada en cada envío. No analiza todo el informe ni investiga automáticamente.',
  nextTitle: 'Próximos pasos',
  nextDescription:
    'Revisa las pruebas antes de investigar o repetir la observación.',
  investigateAction: 'Investigar cambios relacionados',
  why: 'Por qué falló la verificación',
  failedFallback:
    'Las reglas confirman un fallo. Consulta los datos técnicos para conocer los detalles disponibles.',
  unknownTitle: 'Aún sin confirmar',
  unknownScope:
    'Las muestras no prueban disponibilidad continua ni salud funcional. La causa raíz requiere pruebas independientes.',
  unconfirmed:
    'Información sin confirmar; el registro original está en los detalles técnicos.',
  unknown: 'Desconocido',
  unknownTarget: 'Destino sin confirmar',
  unknownCondition: 'Condición desconocida',
  reportMissing: 'No se encontró este informe.',
  technicalDetails: 'Detalles técnicos',
  reportDetails: 'Datos originales del informe',
  evidenceReference: 'Ver prueba {n}',
  evidenceLoaded: '{n} cargadas; solo cuenta las pruebas cargadas.',
  continueFinding: 'Seguir buscando esta prueba',
  find: {
    found: 'Se encontró la prueba citada.',
    pending: 'La prueba aún no está cargada. Continúa página por página.',
    loading: 'Leyendo y localizando pruebas…',
    missing: 'Se llegó a la última página sin encontrar esta prueba.',
    expired: 'Las pruebas caducaron. La conclusión histórica se conserva.',
    error: 'No se pudo leer la prueba. Continúa la búsqueda manualmente.',
    stalled:
      'El cursor no avanzó. Se detuvo la búsqueda; actualiza el informe.',
  },
  kinds: {
    runtime: 'Muestra de ejecución',
    baseline: 'Referencia previa al despliegue',
    expected_image: 'Registro de imagen esperada',
    deployment_health_gate: 'Control de salud del despliegue',
    logs: 'Registros del período',
    unknown: 'Tipo de prueba desconocido',
  },
  running: 'Estado de ejecución',
  runningValue: 'En ejecución',
  stoppedValue: 'Detenido',
  exitCode: 'Código de salida',
  oom: 'Terminación por falta de memoria',
  restarts: 'Número de reinicios',
  yes: 'Sí',
  no: 'No',
  health: 'Estado de salud Docker',
  healthValues: {
    healthy: 'Comprobación Docker superada',
    unhealthy: 'Comprobación Docker fallida',
    starting: 'Comprobación Docker iniciándose',
    absent: 'Sin configurar; salud funcional desconocida',
    unknown: 'Desconocido',
  },
  imageIdentity: 'Identidad de imagen',
  imageRecorded: 'Valor de identidad',
  recorded: 'Registrado; consulta los detalles técnicos',
  identityValues: {
    config_id: 'ID local de imagen Docker',
    manifest: 'Resumen del manifiesto',
    index: 'Resumen del índice',
    repository_digest_unknown: 'Tipo de resumen desconocido',
    unknown: 'Desconocido',
  },
  gate: 'Control del despliegue',
  failed: 'Falló',
  windowStart: 'Inicio de registros',
  windowEnd: 'Fin de registros',
  logGap: 'Hueco en el calendario de recopilación',
  fileStatus: {
    added: 'Añadido',
    modified: 'Modificado',
    deleted: 'Eliminado',
    renamed: 'Renombrado',
    copied: 'Copiado',
    unknown: 'Estado desconocido',
  },
  timeline: {
    deploymentStarted: 'Intento de despliegue iniciado',
    activationStarted: 'Activación del contenedor iniciada',
    deploymentFinished: 'Intento de despliegue finalizado',
    baselineFailing: 'La referencia previa ya registraba un fallo de ejecución',
    logsRequested: 'Recopilación limitada de registros solicitada',
    logsCollected: 'Resultado de registros del período guardado',
    logsUnavailable: 'Registros del período no disponibles',
    finished: 'Tarea de observación finalizada',
    investigated: 'Investigación de cambios actualizada',
    evidenceExpired: 'Pruebas originales caducadas',
  },
  runtimeImageTruncated:
    'Los metadatos de imagen se truncaron. Los hechos de ejecución se conservan; la identidad completa de la imagen no está confirmada.',
  targetBindingChanged:
    'La configuración cambió tras recoger estos hechos. No confirman que la vinculación actual del destino siga siendo válida.',
  targetBindingUnknown:
    'La marca de cambio del destino no es válida. La vinculación actual no está confirmada.',
  logsPartialScope:
    'Solo está disponible el extracto conservado. Puede faltar contenido del período solicitado.',
  logLines: 'Líneas obtenidas',
  reasons: {
    sourceCacheBusy:
      'La caché local de código está ocupada; no se obtuvo la comparación.',
    sourceCacheUnreadable:
      'No se puede leer la caché local; la comparación no está confirmada.',
    baseCommitNotCached:
      'El commit base no está en la caché local; no hay comparación exacta.',
    currentCommitNotCached:
      'El commit actual no está en la caché local; no hay comparación exacta.',
    logTimeGap:
      'Hay un hueco temporal en los registros. Los intervalos ausentes no prueban un funcionamiento normal.',
    serviceRestarted:
      'Un reinicio del servicio interrumpió la observación. Esto no prueba un fallo de la aplicación.',
    resolvedSourceUnavailable:
      'No hay un commit completo y verificable del código; no es posible una comparación exacta.',
    sourceRunUnavailable:
      'La ejecución de origen no está disponible o no se ha verificado; su commit no puede confirmarse.',
    sourceRevisionConflict:
      'Los registros del commit de origen entran en conflicto y no permiten una comparación exacta.',
    sourceRevisionNotConfirmed:
      'Los registros de ejecución y despliegue no confirman de forma coincidente el commit de origen; no hay comparación disponible.',
    currentDeploymentIdentityUnavailable:
      'El registro de identidad del despliegue actual no está disponible; no se puede verificar una base fiable de comparación.',
    deploymentHistoryUnavailable:
      'El historial de despliegues no está disponible; no puede verificarse la base de comparación de cambios.',
  },
  citationLabel: '{candidate} · {context} · Prueba {n}',
}
