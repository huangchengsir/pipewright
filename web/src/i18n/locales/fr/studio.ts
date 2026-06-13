export default {
  // ─── header ────────────────────────────────────────────────────
  brandTitle: 'Pipewright · Bibliothèque',
  brandSubtitle: 'Studio de nœuds personnalisés (Custom Node Studio)',
  namePlaceholder: 'Nommez ce nœud…',
  nameAria: 'Nom du nœud',
  cancel: 'Annuler',
  saving: 'Enregistrement…',
  saveToLibrary: 'Enregistrer dans la bibliothèque',

  // ─── hero ──────────────────────────────────────────────────────
  heroEyebrow: 'Low-code · Définir une fois, réutiliser partout',
  heroTitlePre: 'Composez des étapes paramétrables en les ',
  heroTitleEm: 'faisant glisser',
  heroTitlePost: ' dans un nœud réutilisable',
  heroDescPre: 'Faites glisser des blocs depuis la palette groupée à gauche vers le canevas central et réorganisez les cartes ; à droite, « promouvez » des variables en paramètres de surface du nœud. Le bas compile en temps réel vers un nœud',
  heroDescPost: 'existant — zéro modification du backend, les instances ne configurent que les quelques paramètres promus.',

  // ─── banners ───────────────────────────────────────────────────
  loadingBanner: 'Chargement…',
  loadFailed: 'Échec du chargement',
  loadFailedCode: 'Échec du chargement ({code})',
  saveFailed: 'Échec de l’enregistrement',
  saveFailedCode: 'Échec de l’enregistrement ({code})',
  errNameRequired: 'Le nom du nœud ne peut pas être vide',
  errNeedCommandStep: 'Au moins une étape doit produire une commande',
  updatedToast: 'Nœud personnalisé « {name} » mis à jour',
  createdToast: 'Nœud personnalisé « {name} » créé',

  // ─── palette ───────────────────────────────────────────────────
  paletteHintPre: 'Faites glisser les blocs ci-dessous ',
  paletteHintStrong: 'dans',
  paletteHintPost: ' le canevas central (ou cliquez pour ajouter à la fin).',

  // ─── compose canvas ────────────────────────────────────────────
  composeTitle: 'Composition d’étapes · Faites glisser les cartes pour réorganiser',
  composeEmpty: 'Faites glisser des blocs ici pour commencer →',
  moveUp: 'Monter',
  moveDown: 'Descendre',
  deleteStep: 'Supprimer',

  // ─── step field labels ─────────────────────────────────────────
  fieldCommandMultiline: 'Commande (multiligne)',
  fieldInstallCommand: 'Commande d’installation',
  fieldEchoText: 'Texte d’écho',
  phEchoText: 'Démarrage de la compilation…',
  fieldEnvKey: 'Nom de variable',
  fieldEnvValue: 'Valeur',
  fieldTargetDir: 'Répertoire cible',
  fieldPathDir: 'Répertoire à ajouter au PATH',
  fieldArtifactPath: 'Chemin de l’artefact (glob)',
  fieldSaveAs: 'Enregistrer sous',
  fieldArchiveFile: 'Fichier d’archive',
  fieldExtractTo: 'Extraire vers le répertoire',
  fieldCondition: 'Condition shell (ignore la suite si fausse, sûr avec set -e)',
  fieldCommand: 'Commande',
  fieldRetryCount: 'Nombre',
  fieldDelaySecs: 'Intervalle (s)',
  fieldTimeoutSecs: 'Délai (s)',
  fieldSleepSecs: 'Attente (s)',
  fieldProbeUrl: 'URL de sonde',
  fieldNote: 'Note (compilée en commentaire #, non exécutée)',
  fieldTestCommand: 'Commande de test',
  fieldReportPath: 'Chemin du rapport (JUnit)',
  fieldMinCoverage: 'Seuil de couverture %',

  // ─── right rail tabs ───────────────────────────────────────────
  tabParams: 'Paramètres promus',
  tabMeta: 'Surface du nœud',

  // ─── params tab ────────────────────────────────────────────────
  paramsHintPre: 'Référencés dans les étapes sous la forme',
  paramsHintPost: '; lors de la réutilisation d’une instance, seuls ceux-ci sont configurés.',
  paramsEmpty: 'Aucun paramètre promu pour l’instant. Coder le script en dur est possible ; promouvez une valeur pour pouvoir la modifier par instance.',
  removeParamAria: 'Retirer le paramètre',
  newParamLabel: 'Nouveau paramètre',
  phDisplayLabel: 'Libellé affiché',
  phDefaultValue: 'Valeur par défaut',
  phOptions: 'Options séparées par des virgules, ex. 20, 18, 22',
  addParam: '＋ Promouvoir un paramètre',

  // ─── param type options ────────────────────────────────────────
  paramTypeText: 'Texte',
  paramTypeSelect: 'Énumération',
  paramTypeNumber: 'Nombre',
  paramTypeToggle: 'Booléen',

  // ─── meta tab ──────────────────────────────────────────────────
  metaImagePre: 'Image d’exécution (peut contenir ',
  metaImagePost: ')',
  metaIcon: 'Icône',
  metaCategory: 'Catégorie',
  phCategory: 'Compilation et artefacts',
  metaSummaryPre: 'Résumé en une ligne (peut contenir ',
  metaSummaryPost: ')',
  metaHint: 'La catégorie détermine dans quel groupe il apparaît dans le sélecteur « Ajouter un nœud » ; le résumé + l’icône sont la carte que les réutilisateurs voient en premier.',
  defaultCategory: 'Personnalisé',

  // ─── bottom: compiled output ───────────────────────────────────
  compiledTitle: 'Sortie compilée · config de nœud templated (le backend l’exécute tel quel)',
  undeclaredWarn: '⚠ Les étapes référencent des paramètres non promus : {refs} (conservés tels quels, non modifiables par instance)',
  compiledComment: '# config de nœud personnalisé templated —— le backend l’exécute dans un conteneur après renderTemplate({open})',
  compiledEmpty: '(Aucune étape pour l’instant)',

  // ─── bottom: instance preview ──────────────────────────────────
  previewTitle: 'Aperçu de l’instance · Voici tout ce qui apparaît après l’avoir glissé dans un pipeline',
  unnamedNode: 'Nœud sans nom',
  customLabel: 'Personnalisé',
  previewNote: 'C’est exactement le paradigme n8n « promouvoir des paramètres → liste courte d’instance » / propriétés de Subflow Node-RED : les réutilisateurs n’ont pas besoin de comprendre le script interne, seulement les paramètres exposés.',
}
