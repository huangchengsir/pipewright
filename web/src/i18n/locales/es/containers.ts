export default {
  title: 'Contenedores',
  subtitle: 'Gestiona contenedores e imágenes por host en todos los servidores registrados',
  countSummary: '· {total} contenedores en total · {running} en ejecución',
  autoRefresh: '· Se actualiza cada {n} s',

  aiAssistant: '✦ Asistente de IA',
  prune: '🧹 Limpiar',
  bulkEnter: 'Lote',
  bulkExit: 'Salir del lote',
  create: '+ Nuevo contenedor',

  loadingAria: 'Cargando lista de contenedores',
  errTitle: 'No se pudo cargar la lista de contenedores',
  errConnect: 'No se puede conectar con el servidor. Comprueba que el backend esté en ejecución e inténtalo de nuevo.',
  errLoadStatus: 'No se pudo cargar la lista de contenedores ({status})',
  errLoadRetry: 'No se pudo cargar la lista de contenedores. Inténtalo de nuevo más tarde.',

  emptyTitle: 'Aún no hay servidores registrados',
  emptyDesc: 'Registra los servidores de destino en «Ajustes › Servidores» y sus contenedores e imágenes se agruparán aquí.',

  kpiTotal: 'Contenedores totales',
  kpiRunning: 'En ejecución',
  kpiStopped: 'Detenidos',
  kpiHosts: 'Servidores con contenedores',
  kpiStripAria: 'Estadísticas agregadas de contenedores',

  filterAria: 'Filtrar contenedores por estado',
  filterAll: 'Todos',
  filterRunning: 'En ejecución',
  filterStopped: 'Detenidos',
  filterPaused: 'En pausa',

  searchPlaceholder: 'Buscar contenedores por nombre / imagen',
  searchAria: 'Buscar contenedores por nombre o imagen',
  searchClear: 'Borrar búsqueda',

  bulkAria: 'Acciones por lote',
  bulkSelected: 'Seleccionados',
  bulkSelectedUnit: '',
  bulkClear: 'Borrar selección',
  actionStart: 'Iniciar',
  actionStop: 'Detener',
  actionRestart: 'Reiniciar',
  actionDelete: 'Eliminar',

  confirmTitle: '¿{label} en lote {n} contenedores?',
  confirmBodyRm: 'Los contenedores seleccionados se eliminarán (docker rm). Los contenedores en ejecución deben detenerse primero; de lo contrario, la eliminación fallará (se cuenta como fallo).',
  confirmBodyAction: 'La acción {label} se ejecutará en los {n} contenedores seleccionados; los servicios relacionados pueden interrumpirse brevemente.',
  confirmLabel: '{label} {n}',

  toastDone: '{label} en lote completado',
  toastDoneDetail: '{n} con éxito',
  toastFail: '{label} en lote fallido',
  toastFailDetail: '{n} fallidos',
  toastPartial: '{label} en lote completado parcialmente',
  toastPartialDetail: '{ok} con éxito · {fail} fallidos',

  cardsAria: 'Tarjetas de contenedores por servidor',
  aiContextContainer: '(host de docker)',
}
