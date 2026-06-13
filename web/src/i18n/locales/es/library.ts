export default {
  // ─── page header ───────────────────────────────────────────────
  title: 'Biblioteca',
  subtitle: 'Plantillas de pipeline, grupos de variables y nodos personalizados compartidos entre proyectos · define una vez, reutiliza en todas partes',
  newGroup: '+ Nuevo grupo de variables',
  newStudioNode: '+ Nuevo nodo de estudio',

  // ─── segmented tabs ────────────────────────────────────────────
  segAria: 'Categorías de la biblioteca',
  tabTemplates: 'Plantillas de pipeline',
  tabVariableGroups: 'Grupos de variables',
  tabCustomNodes: 'Nodos personalizados',

  // ─── common ────────────────────────────────────────────────────
  retry: 'Reintentar',
  delete: 'Eliminar',
  edit: 'Editar',
  cancel: 'Cancelar',
  save: 'Guardar',
  saving: 'Guardando…',
  close: 'Cerrar',
  remove: 'Quitar',
  noDescription: 'Sin descripción',
  emptyValue: 'vacío',
  updatedAt: 'Actualizado {time}',

  // ─── templates ─────────────────────────────────────────────────
  emptyTemplatesTitle: 'Aún no hay plantillas de pipeline',
  emptyTemplatesHint: 'Las plantillas te permiten capturar una definición de pipeline y aplicarla con un clic en el editor de pipeline de cualquier proyecto. Puedes guardar el pipeline actual como plantilla desde el editor de pipeline de un proyecto.',
  stageCount: '{n} etapas',

  // ─── variable groups ───────────────────────────────────────────
  emptyGroupsTitle: 'Aún no hay grupos de variables',
  emptyGroupsHint: 'Define un conjunto de variables compartidas (como las mismas direcciones de entorno o referencias de tokens) como un grupo de variables y reutilízalo en varios pipelines. Las variables secretas solo almacenan una referencia al vault, nunca el texto plano.',
  varCount: '{n} variables',
  secretRefTitle: 'Referencia al vault, el texto plano no es visible',
  moreVars: '+{n} variables más…',
  noVars: 'Sin variables',

  // ─── custom nodes ──────────────────────────────────────────────
  emptyNodesTitle: 'Aún no hay nodos personalizados',
  emptyNodesHint: 'Haz clic en "Nuevo nodo de estudio" en la parte superior derecha para combinar pasos y promover parámetros de forma low-code en un nodo reutilizable; o configura cualquier nodo en el editor de pipeline y haz clic en "Guardar como nodo personalizado". Después podrás reutilizarlo con un clic desde el selector de nodos en cualquier pipeline.',
  moreParams: '+{n} parámetros más…',
  noParams: 'Sin parámetros',

  // ─── variable-group editor ─────────────────────────────────────
  createGroup: 'Nuevo grupo de variables',
  editGroup: 'Editar grupo de variables',
  fieldName: 'Nombre',
  fieldDescriptionOptional: 'Descripción (opcional)',
  fieldVariables: 'Variables',
  addVariable: '+ Añadir variable',
  groupNamePlaceholder: 'p. ej. prod-shared-env',
  groupDescPlaceholder: 'Para qué sirve este grupo de variables',
  selectCredential: 'Seleccionar credencial…',
  secretToggleOn: 'Secreto del vault (haz clic para volver a texto plano)',
  secretToggleOff: 'Texto plano (haz clic para convertir en secreto)',

  // ─── custom-node editor ────────────────────────────────────────
  editNode: 'Editar nodo personalizado',
  fieldSummaryOptional: 'Resumen (opcional)',
  fieldUnderlyingType: 'Tipo subyacente',
  underlyingTypeHint: 'El tipo de tarea subyacente no se puede cambiar',
  fieldParams: 'Parámetros',
  addParam: '+ Añadir parámetro',
  nodeNamePlaceholder: 'p. ej. build-and-push',
  nodeDescPlaceholder: 'Para qué sirve este nodo',
  nodeSummaryPlaceholder: 'Resumen de una línea que se muestra en la tarjeta',
  noParamsHint: 'Aún no hay parámetros. Haz clic en "+ Añadir parámetro" para agregar uno.',

  // ─── confirms / toasts / errors ────────────────────────────────
  confirmDeleteTemplate: '¿Eliminar la plantilla «{name}»? Esta acción no se puede deshacer.',
  deletedTemplate: 'Plantilla «{name}» eliminada',
  confirmDeleteGroup: '¿Eliminar el grupo de variables «{name}»? Esta acción no se puede deshacer.',
  deletedGroup: 'Grupo de variables «{name}» eliminado',
  createdGroup: 'Grupo de variables «{name}» creado',
  updatedGroup: 'Grupo de variables «{name}» actualizado',
  confirmDeleteNode: '¿Eliminar el nodo personalizado «{name}»? Esta acción no se puede deshacer.',
  deletedNode: 'Nodo personalizado «{name}» eliminado',
  updatedNode: 'Nodo personalizado «{name}» actualizado',
  groupNameRequired: 'El nombre del grupo de variables no puede estar vacío',
  nodeNameRequired: 'El nombre del nodo personalizado no puede estar vacío',
  deleteFailed: 'Error al eliminar',
  saveFailed: 'Error al guardar',
  saveFailedStatus: 'Error al guardar ({status})',
  loadTemplatesFailed: 'Error al cargar las plantillas',
  loadTemplatesFailedStatus: 'Error al cargar las plantillas ({status})',
  loadGroupsFailed: 'Error al cargar los grupos de variables',
  loadGroupsFailedStatus: 'Error al cargar los grupos de variables ({status})',
  loadNodesFailed: 'Error al cargar los nodos personalizados',
  loadNodesFailedStatus: 'Error al cargar los nodos personalizados ({status})',
}
