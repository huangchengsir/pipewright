export default {
  healthConditionChanged:
    "La condición de salud de Docker cambió; no se verifican de nuevo las condiciones originales.",
  healthComparisonUnknown:
    "No se puede verificar la condición de salud original; no se demuestra que sea la misma.",
  identityMissing:
    "Falta una identidad verificable del contenedor o la conexión.",
  digestMissing: "No se puede verificar el resumen del repositorio.",
  targetChanged:
    "La identidad o configuración cambió; la vinculación no puede continuar.",
  replaced:
    "Un despliegue posterior sustituyó el destino y cerró la ventana anterior.",
  notFound: "El contenedor vinculado ya no existe.",
  exited: "El contenedor terminó o fue detenido por OOM.",
  restarted: "Se observó un reinicio.",
  versionMatched: "Coincide el resumen esperado del repositorio.",
  oldVersion: "Se confirmó otra versión tras el plazo de preparación.",
  restartStable: "No se observó un aumento de reinicios.",
  running: "El contenedor estaba en ejecución en esta muestra.",
  healthGrace: "El margen de inicio de salud sigue activo.",
  healthy: "Docker informa estado saludable.",
  unhealthy: "Docker informa estado no saludable.",
  healthUnknown: "No se pudo determinar la salud Docker.",
  healthAbsent:
    "No hay comprobación de salud Docker; no se verifica la salud del negocio.",
  collectionUnavailable:
    "No se obtuvo una muestra válida. Esto por sí solo no prueba un fallo de la aplicación.",
  truncated: "El resultado recogido está truncado.",
  deadline: "Se alcanzó el plazo de observación.",
  userStopped: "El usuario solicitó detenerla.",
  interrupted: "El servicio interrumpió la observación.",
  persistenceError: "No se pudieron guardar las evidencias.",
  runtimeSample: "Muestra estructurada de ejecución",
  baselineSample: "Muestra inmediatamente anterior al despliegue",
  durationChanged: "La duración cambió.",
  logChanged: "El alcance de registros cambió.",
  targetsChanged: "El alcance de destinos cambió.",
  environmentChanged: "El entorno cambió.",
  rolledBack:
    "Se revirtió el despliegue; la versión nueva esperada no está confirmada.",
  outcomeUnknown: "El resultado del despliegue es desconocido.",
  deploymentFailed: "El despliegue falló.",
  pendingActivation: "La identidad real se vinculará tras la activación.",
};
