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
