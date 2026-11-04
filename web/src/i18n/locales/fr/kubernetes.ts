export default {
  groups: {
    resources: "Ressources et livraison",
    integrations: "Intégrations et IA",
    security: "Sécurité et compte",
    system: "Système et notifications",
  },
  close: "Fermer",
  cancel: "Annuler",
  confirm: "Confirmer et continuer",
  save: "Enregistrer",
  edit: "Modifier",
  delete: "Supprimer",
  remove: "Retirer",
  name: "Nom",
  select: "Sélectionner…",
  none: "Aucun",
  loading: "Chargement…",
  enabled: "Activé",
  disabled: "Désactivé",
  enable: "Activer",
  disable: "Désactiver",
  credentials: "Identifiants Kubernetes",
  addCredential: "Ajouter un identifiant Kubernetes",
  rotate: "Renouveler les identifiants",
  authentication: "Authentification",
  serverToken: "Serveur et jeton",
  context: "Contexte",
  server: "URL du serveur API",
  ca: "Certificat CA (PEM)",
  token: "Jeton d’accès",
  tlsName: "Nom TLS du serveur (facultatif)",
  preview: "Vérifier le format",
  credential: "Identifiant",
  identity: "Détails techniques",
  addConnection: "Ajouter une connexion",
  editConnection: "Modifier la connexion",
  emptyConnections: "Connecter un cluster",
  manageCredentials: "Gérer les identifiants du cluster",
  grants: "Cibles autorisées",
  addGrant: "Ajouter une autorisation",
  grantTitle: "Configurer l’autorisation",
  project: "Projet",
  environment: "Environnement",
  namespace: "Espace de noms",
  deployment: "Deployment",
  container: "Conteneur",
  containers: "Conteneurs autorisés",
  executor: "Exécuteur",
  executors: "Exécuteurs autorisés",
  local: "Docker local",
  sshServer: "Serveur SSH",
  addSSH: "Ajouter un exécuteur SSH",
  hostKey: "Empreinte de l’hôte SSH",
  profile: "Profil de l’outil",
  toolImage: "Image de l’outil (digest fixe)",
  architecture: "Architecture",
  kubectlPath: "Chemin kubectl",
  registryCredential: "Identifiant du registre (facultatif)",
  approvalRequired: "Approbation requise",
  approvalOptional: "Selon la politique du pipeline",
  probe: "Vérifier la connexion",
  replicas: "Réplicas prêts",
  target: "Cible de déploiement",
  connection: "Connexion au cluster",
  manageConnections: "Gérer connexions et autorisations",
  jobTitle: "Déploiement Kubernetes",
  imageSource: "Source de l’image",
  more: "Autres options",
  timeout: "Délai de disponibilité (secondes)",
  passwordTitle: "Confirmez votre identité",
  password: "Mot de passe actuel",
  unsaved: "Abandonner les modifications ?",
  discard: "Abandonner",
  receipt: "Résultat du déploiement",
  reconcile: "Vérifier l’état du cluster",
  retire: "Arrêter le suivi local",
  retired: "Suivi terminé",
  operation: "ID d’opération",
  sourceRun: "Exécution source",
  intent: "Empreinte de l’intention",
  revision: "Révision",
  reason: "Code de diagnostic",
  phases: "Étapes du déploiement",
  recorded: "Enregistré",
  notRecorded: "Non enregistré",
  credentialsHint:
    "Authentification des clusters, distincte des identifiants Git et SSH.",
  emptyCredentials: "Aucun identifiant Kubernetes configuré.",
  secretHint:
    "Les secrets sont effacés après enregistrement ou annulation. Le renouvellement exige des identifiants complets, sans affichage en clair.",
  previewReady: "Format valide ; la connexion au cluster n’a pas été testée",
  connectionsHint:
    "Déployez vers des Deployments existants via des connexions fiables et des autorisations précises.",
  setupHint:
    "Ajoutez les identifiants au coffre, puis l’image de l’outil et les cibles autorisées.",
  grantHint:
    "Autoriser uniquement ce projet, cet environnement, cette charge, ces conteneurs et cet exécuteur.",
  emptyGrants:
    "Aucune cible autorisée. Ajoutez une autorisation avant de vérifier ou déployer.",
  containersHint:
    "Séparez les noms par des virgules. Les jokers sont interdits.",
  hostKeyHint:
    "Vérifiez l’empreinte SHA256 via un canal fiable. Une correspondance exacte est exigée.",
  profileHint:
    "L’enregistrement ne valide pas l’outil. Vérifiez la connexion pour valider son exécution.",
  probeHint:
    "Lecture seule de la cible autorisée choisie, sans mise à jour ni déploiement.",
  probeReady: "Connexion vérifiée ; cible lue",
  jobDescription:
    "Mettre à jour l’image d’un Deployment existant et attendre sa disponibilité.",
  separateStage:
    "Une étape dédiée sans matrice, nœuds parallèles ni tâches annexes est nécessaire. Utilisez Ajouter un nœud.",
  noProjectGrant:
    "Aucune autorisation pour ce projet. Ajoutez-en une dans les paramètres.",
  staleTarget: "Ancienne cible indisponible ; vérifiez les autorisations",
  unavailableChoice: "Sélection précédente indisponible",
  imageSourceHint:
    "Choisissez un nœud de publication en amont. Son digest réel du registre sera utilisé.",
  noPushSource:
    "Aucun nœud de publication en amont. Ajoutez-en un et reliez-le.",
  passwordHint:
    "Cette action exige un mot de passe vérifié depuis moins de 5 minutes.",
  unsavedBody: "Quitter efface le formulaire en cours.",
  deleteBody:
    "Supprimer « {name} » ? Les objets utilisés ne peuvent pas être supprimés.",
  noReceipt:
    "Aucun résultat unique pour ce nœud. Impossible de confirmer succès ou relance.",
  retireBody:
    "Arrête le suivi local pour permettre une publication complète, sans retour arrière ni modification du résultat d’origine.",
  unknownHint:
    "Le déploiement a pu être appliqué. Vérifiez avant toute nouvelle soumission.",
  cleanupHint:
    "Le nettoyage du conteneur outil n’est pas confirmé ; relance bloquée.",
  historyHint:
    "La vérification ne modifie pas l’exécution d’origine. La suivante doit revalider la cible.",
  notRecordedHint:
    "Aucune preuve pour cette étape ; son démarrage ou succès n’est pas confirmé.",
  noRollback:
    "Cet historique contient des cibles Kubernetes ; le retour SSH est indisponible. Publiez via une nouvelle exécution complète.",
  adopted: "Déploiement source revalidé et réutilisé",
  rolloutOnly:
    "Seule la disponibilité du déploiement est vérifiée, sans surveillance prolongée.",
  phase: {
    preflight: "Vérifications initiales",
    update: "Mise à jour de l’image",
    rollout: "Attente de disponibilité",
    cleanup: "Nettoyage des outils",
  },
  phaseCode: {
    patch_not_authorized:
      "Ces identifiants de cluster ne permettent pas de mettre à jour ce Deployment. Demandez à un administrateur de vérifier les droits. Aucune mise à jour n’a été envoyée.",
    permission_check_unverified:
      "Les droits de déploiement sur le cluster n’ont pas pu être vérifiés. Vérifiez la connexion et la configuration du contrôle des droits. Aucune mise à jour n’a été envoyée.",
    preflight_started:
      "Vérification des autorisations, de l’outil et de la cible.",
    image_update_dispatched:
      "Demande de mise à jour envoyée ; le résultat dépend du reçu.",
    rollout_observing: "Observation de la disponibilité des réplicas.",
    rollout_ready:
      "Cette observation a confirmé la disponibilité de la charge.",
    cleanup_started: "Nettoyage du conteneur outil de cette opération.",
    cleanup_verified: "Nettoyage de l’outil confirmé.",
    cleanup_unverified: "Nettoyage non confirmé ; consultez le résultat.",
  },
  business: {
    not_dispatched: "Préparation du déploiement",
    submitted: "Soumis, en attente de disponibilité",
    ready: "Charge disponible",
    unchanged: "Version déjà présente, disponibilité vérifiée",
    failed: "Déploiement incomplet",
    superseded: "Cible remplacée par une autre modification",
    outcome_unknown: "Résultat du déploiement non confirmé",
  },
  cleanup: {
    pending: "Nettoyage en attente",
    verified: "Nettoyage confirmé",
    blocked: "Nettoyage bloqué",
  },
  errors: {
    invalid: "Configuration invalide. Vérifiez les champs.",
    session: "Session expirée. Reconnectez-vous.",
    passwordRequired:
      "Vérifiez à nouveau votre mot de passe avant cette action.",
    passwordInvalid:
      "Échec de vérification du mot de passe. Vérifiez et réessayez.",
    denied: "Connexion ou cible non autorisée.",
    missing: "Enregistrement introuvable. Actualisez la liste.",
    conflict:
      "Configuration modifiée ailleurs. Brouillon conservé ; actualisez avant d’enregistrer.",
    inUse:
      "Cet enregistrement est encore référencé et ne peut pas être modifié.",
    held: "Un déploiement inachevé occupe cette cible. Vérifiez l’opération d’origine.",
    ownerUnknown:
      "L’arrêt de l’exécuteur d’origine n’est pas confirmé. Reprise et relance bloquées.",
    runtime:
      "Échec des contrôles de sécurité. Vérifiez l’outil et l’exécuteur.",
    credentialUnavailable:
      "Identifiants du cluster indisponibles. Vérifiez le coffre et la connexion.",
    observation:
      "Observation interrompue, résultat non confirmé. Consultez l’opération d’origine.",
    unavailable: "Service d’exécution indisponible. Vérifiez la connexion.",
    uncertain:
      "Résultat incertain ; renvoi bloqué. Actualisez les enregistrements. Un nom identique ne prouve pas la création.",
    load: "Chargement impossible. Actualisez pour réessayer.",
  },
  readBack: "Relire les enregistrements",
  acknowledge: "Vérifié ; terminer la modification",
  readBackHint:
    "Derniers enregistrements chargés. Vérifiez l’identité et la révision ; le résultat d’origine reste à confirmer.",
  targetCount: "{n} cibles de déploiement",
};
