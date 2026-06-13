export default {
  // ─── barre supérieure / fil d'Ariane ────────────────────────────
  breadcrumbAria: "Navigation par fil d'Ariane",
  breadcrumbProjects: 'Projets',
  title: 'Configuration du pipeline',

  // ─── onglets ────────────────────────────────────────────────────
  tabCanvas: 'Canevas du pipeline',
  tabVars: 'Variables et cache',
  tabTriggers: 'Paramètres de déclenchement',
  tabEnvs: 'Environnements et identifiants',
  tabStripAria: 'Onglets de configuration du pipeline',

  // ─── Pipeline en tant que code (GitOps · FR-8-12) ───────────────
  pacTitle: 'Pipeline en tant que code',
  pacOnHint: "Les exécutions lisent .pipewright.yml depuis la branche d'exécution du dépôt (repli sur cette configuration si le fichier est absent ou invalide).",
  pacOffHint: "Une fois activé, chaque exécution lit .pipewright.yml à la racine du dépôt sur la branche d'exécution (repli sur cette configuration si absent ou invalide).",
  pacToggleFailed: 'Échec du basculement. Veuillez réessayer.',

  // ─── Vérifications de statut de PR (réécriture du statut de commit · Story 8-9 / FR-8-9) ─
  prStatusTitle: 'Vérifications de statut de PR',
  prStatusOnHint: "À la fin d'une exécution, Pipewright détecte la plateforme du dépôt (GitHub/Gitee) et réécrit le statut de réussite/échec du commit en tant que vérification de PR à l'aide de l'identifiant du projet (au mieux ; les échecs n'affectent jamais l'exécution).",
  prStatusOffHint: "Une fois activé, la fin d'une exécution détecte la plateforme du dépôt (GitHub/Gitee) et réécrit le statut du commit en tant que vérification de PR à l'aide de l'identifiant du projet (au mieux).",
  prStatusToggleFailed: 'Échec du basculement. Veuillez réessayer.',

  // ─── Aperçu de la config du dépôt (GitOps · récupérer et valider .pipewright.yml à une réf) ──
  pacPreviewBtn: 'Aperçu de la config du dépôt',
  pacPreviewTitle: 'Aperçu de la configuration du dépôt',
  pacPreviewSub: 'Récupérez et validez le fichier du dépôt à une branche/un tag/un commit :',
  pacPreviewCloseAria: "Fermer l'aperçu",
  pacPreviewCloseBtn: 'Fermer',
  pacPreviewRefLabel: 'Branche / tag / commit',
  pacPreviewFetch: 'Récupérer et valider',
  pacPreviewNotFound: "Aucun .pipewright.yml trouvé à {ref} ; les exécutions se rabattront sur le pipeline configuré ici.",
  pacPreviewInvalid: "Le .pipewright.yml à {ref} a échoué à la validation ; les exécutions se rabattront silencieusement sur le pipeline configuré ici :",
  pacPreviewValid: 'Le .pipewright.yml à {ref} est valide avec {count} étape(s) ; les exécutions l\'utiliseront.',
  pacPreviewJobCount: '{count} tâche(s)',
  pacPreviewConnFailed: 'Échec de la connexion. Vérifiez votre réseau et réessayez.',
  pacPreviewFailed: "Échec de l'aperçu ({status}). Veuillez réessayer.",
  pacPreviewFailedRetry: "Échec de l'aperçu. Veuillez réessayer.",

  // ─── boutons de la barre d'outils ───────────────────────────────
  aiGenerate: 'Générer le pipeline par IA',
  importYaml: 'Importer depuis YAML',
  templates: 'Modèles',
  validate: 'Valider la configuration',
  closeValidationPanel: 'Fermer le panneau de validation',
  badgeReady: 'Prêt',
  badgeErrors: '{n} erreurs',
  saving: 'Enregistrement…',
  saveDraft: 'Enregistrer le brouillon',

  // ─── bannières / état ───────────────────────────────────────────
  dismiss: 'Ignorer',
  draftSaved: 'Brouillon du pipeline enregistré',
  retry: 'Réessayer',
  loading: 'Chargement',

  // ─── erreurs de chargement ──────────────────────────────────────
  errNoServer: 'Impossible de joindre le serveur. Vérifiez que le backend est en cours d’exécution puis réessayez.',
  errProjectNotFound: 'Le projet est introuvable. Vérifiez que l’ID du projet est correct.',
  errLoadFailedStatus: 'Échec du chargement du pipeline ({status})',
  errLoadFailedRetry: 'Échec du chargement du pipeline. Veuillez réessayer plus tard.',

  // ─── erreurs d'enregistrement ───────────────────────────────────
  errSaveFailedRetry: 'Échec de l’enregistrement. Veuillez réessayer plus tard.',
  errSaveFailedStatus: 'Échec de l’enregistrement ({status})',
  errInvalidStage: 'Le nom de l’étape ne peut pas être vide et kind doit être une valeur autorisée. Vérifiez puis réessayez.',
  errInvalidJob: 'Le nom ou le type de la tâche ne peut pas être vide. Complétez-les puis réessayez.',
  errDuplicateId: 'ID d’étape ou de tâche en double. Supprimez les doublons puis réessayez.',
  errInvalidBuild: 'Le modèle de build doit être dockerfile/toolchain et le type d’artefact doit être image/jar/dist.',
  errInvalidVar: 'La clé de variable ne peut pas être vide et doit être unique dans sa portée ; les variables secret nécessitent un identifiant du coffre-fort.',
  errInvalidEnvironment: 'Le nom de l’environnement ne peut pas être vide et le type de registre d’images doit être harbor/acr/dockerhub/custom.',
  errCredentialNotFound: 'L’identifiant du coffre-fort référencé n’existe pas. Resélectionnez-le puis réessayez.',
  errVaultUnconfigured: 'Le coffre-fort n’a pas de master key configurée, les identifiants secret ne peuvent donc pas être référencés.',
}
