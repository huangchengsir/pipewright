export default {
  title: 'Métricas DORA',
  subtitle:
    'Vista de rendimiento de entrega agregada a partir de los datos de ejecución existentes · Frecuencia de despliegue / Tiempo de entrega / Tasa de fallos de cambios / Tiempo de restauración',
  generatedAt: '· Datos hasta {time}',

  window7d: 'Últimos 7 días',
  window30d: 'Últimos 30 días',
  window90d: 'Últimos 90 días',

  projectLabel: 'Proyecto',
  projectFilterAria: 'Filtrar por proyecto',
  allProjects: 'Todos los proyectos',
  windowAria: 'Ventana temporal',

  errTitle: 'Error al cargar las métricas DORA',
  errOffline: 'No se puede conectar con el servidor. Comprueba que el backend esté en ejecución e inténtalo de nuevo',
  errLoadStatus: 'Error al cargar las métricas DORA ({status})',
  errLoadRetry: 'Error al cargar las métricas DORA. Inténtalo de nuevo más tarde',

  summaryDeployments: 'Despliegues en {days} días',
  summarySuccess: 'Con éxito',
  summaryFailed: 'Fallidos',

  metricDeployFreq: 'Frecuencia de despliegue',
  metricLeadTime: 'Tiempo de entrega',
  metricCfr: 'Tasa de fallos de cambios',
  metricMttr: 'Tiempo medio de restauración',

  capDeployFreq: '{count} despliegues con éxito en {days} días',
  capLeadTime: 'Mediana del tiempo de commit→producción en {count} despliegues con éxito',
  capLeadTimeEmpty: 'Aún no hay despliegues con éxito para calcular el tiempo de entrega',
  capCfr: '{failed} / {total} despliegues fallidos',
  capMttr: 'Mediana de duración de {count} pares «fallo→restauración»',
  capMttrEmpty: 'No hay pares «fallo→restauración» en esta ventana',

  noteLead:
    'Metodología: un «despliegue» = una ejecución que llega a un estado terminal; cuando falta la hora del commit, el tiempo de entrega se aproxima con la hora de encolado. Las métricas DORA derivadas de los datos de ejecución de CI son una ',
  noteEmphasis: 'aproximación',
  noteTrail: ' de referencia, no una base para un SLA.',
}
