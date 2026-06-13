export default {
  title: 'Entornos',
  subtitle:
    'Historial de despliegues agrupado por entorno y la versión activa actual · revierte al último despliegue correcto con un clic',
  project: 'Proyecto',
  filterByProject: 'Filtrar por proyecto',
  selectProject: 'Selecciona un proyecto',

  emptySelectTitle: 'Selecciona un proyecto',
  emptySelectDesc: 'El historial de despliegues se agrupa por proyecto: elige uno arriba primero.',
  emptyTitle: 'Este proyecto aún no tiene historial de despliegues',
  emptyDesc:
    'Cuando se ejecute un despliegue en un entorno (el mapeo de ramas del webhook resuelve el nombre del entorno y el despliegue finaliza), aquí aparecerá la cronología agrupada por entorno.',

  errLoadTitle: 'Error al cargar el historial de despliegues',
  errNetwork: 'No se puede conectar con el servidor. Comprueba que el backend esté en ejecución e inténtalo de nuevo.',
  errLoad: 'Error al cargar el historial de despliegues ({status})',
  errLoadRetry: 'Error al cargar el historial de despliegues. Inténtalo de nuevo más tarde.',

  active: 'Activa',
  activeVersionTitle: 'Versión activa actual',
  noActiveVersion: 'Sin versión activa',
  noFullSuccessTitle: 'Aún no hay un despliegue totalmente correcto',
  targetCount: '{n} hosts de destino',

  rollback: 'Revertir',
  rollbackEnabledTitle: 'Revertir al último despliegue correcto',
  rollbackDisabledTitle: 'No hay un despliegue correcto anterior al que revertir',
  rollbackTitle: 'Revertir el entorno «{env}»',
  rollbackBody:
    'Esto revierte el entorno al último despliegue correcto (ejecución {commit} · {when}), volviendo a desplegar esos artefactos en los hosts de destino originales. Esta acción desencadena un despliegue real.',
  rollbackConfirm: 'Confirmar reversión',
  rollbackFailedStatus: 'Error en la reversión ({status})',
  rollbackFailedRetry: 'Error en la reversión. Inténtalo de nuevo más tarde.',

  toastRolledBack: 'Entorno «{env}» revertido',
  toastRolledBackDetail: 'Artefactos vueltos a desplegar en {n} hosts de destino',
  toastRollbackPartial: 'La reversión del entorno «{env}» falló parcialmente',
  toastRollbackPartialDetail: '{failed}/{total} hosts de destino fallaron',
  toastRollbackFailed: 'Error al revertir el entorno «{env}»',

  timelineAria: 'Historial de despliegues de {env}',
}
