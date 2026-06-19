export default {
  // ─── header ────────────────────────────────────────────────────
  brandTitle: 'Pipewright · Biblioteca',
  brandSubtitle: 'Estudio de Nodos Personalizados (Custom Node Studio)',
  namePlaceholder: 'Nombra este nodo…',
  nameAria: 'Nombre del nodo',
  cancel: 'Cancelar',
  saving: 'Guardando…',
  saveToLibrary: 'Guardar en la biblioteca',

  // ─── hero ──────────────────────────────────────────────────────
  heroEyebrow: 'Low-code · Define una vez, reutiliza en todas partes',
  heroTitlePre: 'Compón pasos parametrizables ',
  heroTitleEm: 'arrastrándolos',
  heroTitlePost: ' en un nodo reutilizable',
  heroDescPre: 'Arrastra bloques desde la paleta agrupada de la izquierda al lienzo central y reordena las tarjetas; a la derecha, «promueve» variables a parámetros de la superficie del nodo. La parte inferior compila en tiempo real en un nodo',
  heroDescPost: 'existente: cero cambios en el backend, las instancias solo configuran los pocos promovidos.',

  // ─── banners ───────────────────────────────────────────────────
  loadingBanner: 'Cargando…',
  loadFailed: 'Error al cargar',
  loadFailedCode: 'Error al cargar ({code})',
  saveFailed: 'Error al guardar',
  saveFailedCode: 'Error al guardar ({code})',
  errNameRequired: 'El nombre del nodo no puede estar vacío',
  errNeedCommandStep: 'Al menos un paso debe producir un comando',
  updatedToast: 'Nodo personalizado «{name}» actualizado',
  createdToast: 'Nodo personalizado «{name}» creado',

  // ─── palette ───────────────────────────────────────────────────
  paletteHintPre: 'Arrastra los bloques de abajo ',
  paletteHintStrong: 'al',
  paletteHintPost: ' lienzo central (o haz clic para añadir al final).',

  // ─── compose canvas ────────────────────────────────────────────
  composeTitle: 'Composición de pasos · Arrastra las tarjetas para reordenar',
  composeEmpty: 'Arrastra bloques aquí para empezar →',
  moveUp: 'Subir',
  moveDown: 'Bajar',
  deleteStep: 'Eliminar',

  // ─── step field labels ─────────────────────────────────────────
  fieldCommandMultiline: 'Comando (varias líneas)',
  fieldInstallCommand: 'Comando de instalación',
  fieldEchoText: 'Texto de eco',
  phEchoText: 'Iniciando compilación…',
  fieldEnvKey: 'Nombre de variable',
  fieldEnvValue: 'Valor',
  fieldTargetDir: 'Directorio destino',
  fieldPathDir: 'Directorio para añadir al PATH',
  fieldArtifactPath: 'Ruta del artefacto (glob)',
  fieldSaveAs: 'Guardar como',
  fieldArchiveFile: 'Archivo comprimido',
  fieldExtractTo: 'Extraer al directorio',
  fieldCondition: 'Condición de shell (si es falsa, omite lo siguiente, segura con set -e)',
  fieldCommand: 'Comando',
  fieldRetryCount: 'Veces',
  fieldDelaySecs: 'Intervalo (seg)',
  fieldTimeoutSecs: 'Tiempo de espera (seg)',
  fieldSleepSecs: 'Espera (seg)',
  fieldProbeUrl: 'URL de sondeo',
  fieldNote: 'Nota (se compila como comentario #, no se ejecuta)',
  fieldTestCommand: 'Comando de prueba',
  fieldReportPath: 'Ruta del informe (JUnit)',
  fieldMinCoverage: 'Umbral de cobertura %',

  // ─── right rail tabs ───────────────────────────────────────────
  tabParams: 'Parámetros promovidos',
  tabMeta: 'Superficie del nodo',

  // ─── params tab ────────────────────────────────────────────────
  paramsHintPre: 'Se referencian en los pasos como',
  paramsHintPost: '; al reutilizar la instancia solo se configuran estos.',
  paramsEmpty: 'Aún no hay parámetros promovidos. Puedes dejar el script fijo; promueve un valor para poder cambiarlo por instancia.',
  removeParamAria: 'Quitar parámetro',
  newParamLabel: 'Nuevo parámetro',
  phDisplayLabel: 'Etiqueta visible',
  phDefaultValue: 'Valor predeterminado',
  phOptions: 'Opciones separadas por comas, p. ej. 20, 18, 22',
  addParam: '＋ Promover un parámetro',

  // ─── param type options ────────────────────────────────────────
  paramTypeText: 'Texto',
  paramTypeSelect: 'Enumeración',
  paramTypeNumber: 'Número',
  paramTypeToggle: 'Booleano',

  // ─── meta tab ──────────────────────────────────────────────────
  metaImagePre: 'Imagen de ejecución (puede contener ',
  metaImagePost: ')',
  metaIcon: 'Icono',
  metaCategory: 'Categoría',
  phCategory: 'Compilación y artefactos',
  metaSummaryPre: 'Resumen de una línea (puede contener ',
  metaSummaryPost: ')',
  imgPlaceholder: 'p. ej. node:20-alpine',
  summaryPlaceholder: 'p. ej. Compilar con npm y generar dist',
  metaHint: 'La categoría determina en qué grupo aparece en el selector «Añadir nodo»; el resumen + el icono son la tarjeta que los reutilizadores ven primero.',
  defaultCategory: 'Personalizado',

  // ─── bottom: compiled output ───────────────────────────────────
  compiledTitle: 'Salida compilada · config de nodo templated (el backend lo ejecuta tal cual)',
  undeclaredWarn: '⚠ Los pasos referencian parámetros no promovidos: {refs} (se conservan tal cual, no editables por instancia)',
  compiledComment: '# config de nodo personalizado templated —— el backend lo ejecuta en un contenedor tras renderTemplate({open})',
  compiledEmpty: '(Aún no hay pasos)',

  // ─── bottom: instance preview ──────────────────────────────────
  previewTitle: 'Vista previa de instancia · Esto es todo lo que se ve tras arrastrarlo a un pipeline',
  unnamedNode: 'Nodo sin nombre',
  customLabel: 'Personalizado',
  previewNote: 'Este es exactamente el paradigma de n8n «promover parámetros → lista corta de instancia» / propiedades de Subflow de Node-RED: los reutilizadores no necesitan entender el script interno, solo los parámetros expuestos.',
}
