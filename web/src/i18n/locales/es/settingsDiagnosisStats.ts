export default {
  title: 'Bucle de retroalimentación del diagnóstico',
  subtitle:
    'Métricas de calidad del diagnóstico con AI: cuantos más comentarios, más preciso es el diagnóstico, y los casos erróneos se convierten en semillas de la base de conocimiento',
  loading: 'Cargando…',
  retry: 'Reintentar',

  emptyTitle: 'Aún no hay comentarios de diagnóstico',
  emptyHint: 'Pulsa 👍/👎 en el panel de diagnóstico con AI de una ejecución fallida y las estadísticas se agregarán aquí',

  accuracy: 'Precisión',
  accuracyAria: 'Precisión {pct}%',

  countTotal: 'Total de comentarios',
  countUp: '👍 Útil',
  countDown: '👎 Por mejorar',

  trendTitle: 'Tendencia reciente',
  trendBarTitle: '{pct}% · {count} elementos',
  countUnit: '{count} elementos',

  correctionsTitle: 'Correcciones recientes (semillas de la base de conocimiento)',
  correctionsEmpty: 'Aún no hay ninguna causa raíz correcta adjunta a un 👎.',
  runLabel: 'Ejecución {id}',

  errLoadFailed: 'Error al cargar ({status})',
  errLoadGeneric: 'No se pudieron cargar las estadísticas de diagnóstico. Inténtalo de nuevo más tarde.',
}
