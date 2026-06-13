export default {
  // ─── page header ───────────────────────────────────────────────
  title: 'Bibliothèque',
  subtitle: 'Modèles de pipeline, groupes de variables et nœuds personnalisés partagés entre projets · définir une fois, réutiliser partout',
  newGroup: '+ Nouveau groupe de variables',
  newStudioNode: '+ Nouveau nœud de studio',

  // ─── segmented tabs ────────────────────────────────────────────
  segAria: 'Catégories de la bibliothèque',
  tabTemplates: 'Modèles de pipeline',
  tabVariableGroups: 'Groupes de variables',
  tabCustomNodes: 'Nœuds personnalisés',

  // ─── common ────────────────────────────────────────────────────
  retry: 'Réessayer',
  delete: 'Supprimer',
  edit: 'Modifier',
  cancel: 'Annuler',
  save: 'Enregistrer',
  saving: 'Enregistrement…',
  close: 'Fermer',
  remove: 'Retirer',
  noDescription: 'Aucune description',
  emptyValue: 'vide',
  updatedAt: 'Mis à jour le {time}',

  // ─── templates ─────────────────────────────────────────────────
  emptyTemplatesTitle: 'Aucun modèle de pipeline pour le moment',
  emptyTemplatesHint: 'Les modèles vous permettent de capturer une définition de pipeline et de l’appliquer en un clic dans l’éditeur de pipeline de n’importe quel projet. Vous pouvez enregistrer le pipeline actuel comme modèle depuis l’éditeur de pipeline d’un projet.',
  stageCount: '{n} étapes',

  // ─── variable groups ───────────────────────────────────────────
  emptyGroupsTitle: 'Aucun groupe de variables pour le moment',
  emptyGroupsHint: 'Définissez un ensemble de variables partagées (comme les mêmes adresses d’environnement ou références de jetons) en tant que groupe de variables et réutilisez-le dans plusieurs pipelines. Les variables secrètes ne stockent qu’une référence au coffre, jamais le texte en clair.',
  varCount: '{n} variables',
  secretRefTitle: 'Référence au coffre, texte en clair non visible',
  moreVars: '+{n} variables de plus…',
  noVars: 'Aucune variable',

  // ─── custom nodes ──────────────────────────────────────────────
  emptyNodesTitle: 'Aucun nœud personnalisé pour le moment',
  emptyNodesHint: 'Cliquez sur « Nouveau nœud de studio » en haut à droite pour composer des étapes et promouvoir des paramètres en low-code dans un nœud réutilisable ; ou configurez n’importe quel nœud dans l’éditeur de pipeline puis cliquez sur « Enregistrer comme nœud personnalisé ». Vous pourrez ensuite le réutiliser en un clic depuis le sélecteur de nœuds de n’importe quel pipeline.',
  moreParams: '+{n} paramètres de plus…',
  noParams: 'Aucun paramètre',

  // ─── variable-group editor ─────────────────────────────────────
  createGroup: 'Nouveau groupe de variables',
  editGroup: 'Modifier le groupe de variables',
  fieldName: 'Nom',
  fieldDescriptionOptional: 'Description (facultative)',
  fieldVariables: 'Variables',
  addVariable: '+ Ajouter une variable',
  groupNamePlaceholder: 'ex. prod-shared-env',
  groupDescPlaceholder: 'À quoi sert ce groupe de variables',
  selectCredential: 'Sélectionner un identifiant…',
  secretToggleOn: 'Secret du coffre (cliquer pour revenir au texte en clair)',
  secretToggleOff: 'Texte en clair (cliquer pour convertir en secret)',

  // ─── custom-node editor ────────────────────────────────────────
  editNode: 'Modifier le nœud personnalisé',
  fieldSummaryOptional: 'Résumé (facultatif)',
  fieldUnderlyingType: 'Type sous-jacent',
  underlyingTypeHint: 'Le type de tâche sous-jacent ne peut pas être modifié',
  fieldParams: 'Paramètres',
  addParam: '+ Ajouter un paramètre',
  nodeNamePlaceholder: 'ex. build-and-push',
  nodeDescPlaceholder: 'À quoi sert ce nœud',
  nodeSummaryPlaceholder: 'Résumé d’une ligne affiché sur la carte',
  noParamsHint: 'Aucun paramètre pour le moment. Cliquez sur « + Ajouter un paramètre » pour en ajouter un.',

  // ─── confirms / toasts / errors ────────────────────────────────
  confirmDeleteTemplate: 'Supprimer le modèle « {name} » ? Cette action est irréversible.',
  deletedTemplate: 'Modèle « {name} » supprimé',
  confirmDeleteGroup: 'Supprimer le groupe de variables « {name} » ? Cette action est irréversible.',
  deletedGroup: 'Groupe de variables « {name} » supprimé',
  createdGroup: 'Groupe de variables « {name} » créé',
  updatedGroup: 'Groupe de variables « {name} » mis à jour',
  confirmDeleteNode: 'Supprimer le nœud personnalisé « {name} » ? Cette action est irréversible.',
  deletedNode: 'Nœud personnalisé « {name} » supprimé',
  updatedNode: 'Nœud personnalisé « {name} » mis à jour',
  groupNameRequired: 'Le nom du groupe de variables ne peut pas être vide',
  nodeNameRequired: 'Le nom du nœud personnalisé ne peut pas être vide',
  deleteFailed: 'Échec de la suppression',
  saveFailed: 'Échec de l’enregistrement',
  saveFailedStatus: 'Échec de l’enregistrement ({status})',
  loadTemplatesFailed: 'Échec du chargement des modèles',
  loadTemplatesFailedStatus: 'Échec du chargement des modèles ({status})',
  loadGroupsFailed: 'Échec du chargement des groupes de variables',
  loadGroupsFailedStatus: 'Échec du chargement des groupes de variables ({status})',
  loadNodesFailed: 'Échec du chargement des nœuds personnalisés',
  loadNodesFailedStatus: 'Échec du chargement des nœuds personnalisés ({status})',
}
