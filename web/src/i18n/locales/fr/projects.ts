export default {
  // ─── page header ───────────────────────────────────────────────
  title: 'Projets',
  subtitle: 'Dépôts Gitee gérés : chaque projet correspond à une configuration de pipeline et à des cibles de déploiement',
  newProject: 'Nouveau projet',
  retry: 'Réessayer',

  // ─── toolbar / search / filter ─────────────────────────────────
  searchFilterAria: 'Rechercher et filtrer les projets',
  searchPlaceholder: 'Rechercher par nom, URL de dépôt ou branche…',
  searchAria: 'Rechercher des projets',
  statusFilterAria: 'Filtrer par statut',
  statusAll: 'Tous les statuts',

  // ─── list states ───────────────────────────────────────────────
  loading: 'Chargement',
  emptyTitle: 'Aucun projet pour l’instant',
  emptyHint: 'Connectez votre premier dépôt Gitee, puis configurez un pipeline et déployez vers les serveurs cibles.',
  noMatchTitle: 'Aucun projet correspondant',
  noMatchHint: 'Ajustez le terme de recherche ou le filtre de statut, puis réessayez.',
  clearFilter: 'Effacer les filtres',
  resultCount: '{n} projets',
  resultCountTotal: '(sur {total})',

  // ─── project card ──────────────────────────────────────────────
  runStatusAria: 'Statut de l’exécution : {status}',
  lastRun: 'Dernière exécution',
  noRun: 'Aucune exécution',
  targetServers: 'Serveurs cibles',
  notBound: 'Non lié',
  credential: 'Identifiant du dépôt',
  credentialRefTitle: 'Référence d’identifiant (pas en clair)',
  updatedAt: 'Mis à jour {time}',

  // ─── card actions ──────────────────────────────────────────────
  actionRunTitle: 'Déclencher une exécution manuellement · {name}',
  actionRunAria: 'Déclencher manuellement une exécution de pipeline pour le projet {name}',
  actionRenameTitle: 'Renommer {name}',
  actionRenameAria: 'Renommer le projet {name}',
  actionCodeTitle: 'Parcourir le code · {name}',
  actionCodeAria: 'Parcourir le code du projet {name}',
  actionPipelineTitle: 'Configuration du pipeline · {name}',
  actionPipelineAria: 'Configurer le pipeline du projet {name}',
  actionDeleteTitle: 'Supprimer {name}',
  actionDeleteAria: 'Supprimer le projet {name}',

  // ─── shared modal ──────────────────────────────────────────────
  closeDialog: 'Fermer la boîte de dialogue',
  cancel: 'Annuler',
  save: 'Enregistrer',
  saving: 'Enregistrement…',

  // ─── trigger modal ─────────────────────────────────────────────
  triggerDialogAria: 'Déclenchement manuel · {name}',
  triggerTitle: 'Déclencher une exécution manuellement',
  triggerSub: '{name} · choisissez une branche et créez une exécution de pipeline immédiatement',
  branch: 'Branche',
  branchHint: '(facultatif, laissez vide pour utiliser la branche par défaut du projet)',
  commit: 'Commit',
  commitHint: '(facultatif, laissez vide pour utiliser le HEAD de la branche)',
  commitPlaceholder: 'ex. a3f1c2d',
  params: 'Paramètres',
  paramsHintTyped: '(remplissez selon la définition ; injectés dans le pipeline comme variables d’environnement)',
  paramsHintFree: '(facultatif, injectés dans le pipeline comme variables d’environnement)',
  triggering: 'Déclenchement…',
  runNow: 'Exécuter maintenant',

  // ─── create modal ──────────────────────────────────────────────
  createSub: 'Connectez un dépôt Gitee et liez un identifiant de dépôt',
  fieldName: 'Nom du projet',
  fieldNamePlaceholder: 'ex. acme-web',
  fieldRepo: 'URL du dépôt',
  fieldCredHint: '(seuls les identifiants de type jeton Git sont affichés, jamais en clair)',
  credLoading: 'Chargement des identifiants…',
  credSelect: 'Sélectionner un identifiant de jeton Git',
  credEmptyPre: 'Aucun identifiant de jeton Git pour l’instant. Rendez-vous dans le',
  credVaultLink: 'coffre d’identifiants',
  credEmptyPost: 'pour en ajouter un.',
  fieldDefaultBranch: 'Branche par défaut',
  fieldDefaultBranchHint: '(facultatif, laissez vide pour la détecter automatiquement via le test de connexion)',
  testConnection: 'Tester la connexion',
  testing: 'Test en cours…',
  testOk: 'Connexion réussie',
  testDetectedBranch: '· branche par défaut {branch}',
  creating: 'Création…',
  createSubmit: 'Créer le projet',

  // ─── rename modal ──────────────────────────────────────────────
  renameTitle: 'Renommer le projet',
  renameSub: 'Modifier le nom affiché du projet',

  // ─── delete modal ──────────────────────────────────────────────
  deleteDialogAria: 'Confirmer la suppression du projet',
  deleteTitle: 'Supprimer le projet',
  deleteSub: 'Cette action est irréversible',
  deleteConfirmPre: 'Voulez-vous vraiment supprimer définitivement le projet',
  deleteConfirmPost: '? Sa configuration de pipeline, son historique d’exécutions et ses références d’identifiants seront tous nettoyés.',
  deleting: 'Suppression…',
  confirmDelete: 'Confirmer la suppression',

  // ─── error messages ────────────────────────────────────────────
  errNetwork: 'Impossible de joindre le serveur. Vérifiez que le backend est en cours d’exécution et réessayez.',
  errNetworkRetry: 'Impossible de joindre le serveur. Veuillez réessayer plus tard.',
  errLoadNetwork: 'Impossible de joindre le serveur. Vérifiez que le backend est en cours d’exécution et réessayez',
  errLoadStatus: 'Échec du chargement des projets ({status})',
  errLoadRetry: 'Échec du chargement des projets, veuillez réessayer plus tard',
  errNameRequired: 'Veuillez saisir un nom de projet',
  errRepoRequired: 'Veuillez saisir une URL de dépôt',
  errRepoFormat: 'Format d’URL de dépôt invalide. Elle doit commencer par https:// ou git{\'@\'}',
  errCredRequired: 'Veuillez sélectionner un identifiant de dépôt',
  errRepoFirst: 'Veuillez d’abord saisir une URL de dépôt',
  errCredFirst: 'Veuillez d’abord sélectionner un identifiant de dépôt',
  errNameEmpty: 'Le nom du projet ne peut pas être vide',

  testErrCredential: 'Erreur d’identifiant : vérifiez que votre jeton d’accès Gitee est valide et mettez-le à jour dans le coffre d’identifiants.',
  testErrUnreachable: 'Dépôt inaccessible : vérifiez que l’URL est correcte et que le dépôt existe et est accessible.',
  testErrVault: 'Le coffre n’a pas de master key configurée ; les identifiants ne peuvent pas être lus.',
  testErrStatus: 'Échec du test de connexion ({status})',
  testErrRetry: 'Échec du test de connexion, veuillez réessayer plus tard.',

  createErrCredField: 'Erreur d’identifiant : vérifiez que le jeton d’accès est valide',
  createErrCredBanner: 'Échec de la validation de l’identifiant. Changez d’identifiant ou mettez-le à jour dans le coffre d’identifiants.',
  createErrRepoField: 'Dépôt inaccessible : vérifiez que l’URL est correcte et accessible',
  createErrRepoBanner: 'URL de dépôt inaccessible, la création a échoué.',
  createErrVault: 'Le coffre n’a pas de master key configurée ; le projet ne peut pas être enregistré.',
  createErrStatus: 'Échec de la création ({status})',
  createErrRetry: 'Échec de la création, veuillez réessayer plus tard.',

  renameErrStatus: 'Échec du renommage ({status})',
  renameErrRetry: 'Échec du renommage, veuillez réessayer plus tard.',

  deleteErrStatus: 'Échec de la suppression ({status})',
  deleteErrRetry: 'Échec de la suppression, veuillez réessayer plus tard.',

  triggerErrNotFound: 'Projet introuvable, veuillez actualiser et réessayer.',
  triggerErrStatus: 'Échec du déclenchement ({status})',
  triggerErrRetry: 'Échec du déclenchement, veuillez réessayer plus tard.',
}
