export default {
  healthConditionChanged:
    "La condition de santé Docker a changé ; les conditions initiales ne sont pas revérifiées.",
  healthComparisonUnknown:
    "La condition de santé initiale ne peut pas être vérifiée ; leur identité n’est pas établie.",
  identityMissing:
    "L’identité vérifiable du conteneur ou de la connexion manque.",
  digestMissing: "L’empreinte du dépôt ne peut pas être vérifiée.",
  targetChanged:
    "L’identité ou la configuration a changé ; la liaison ne peut pas continuer.",
  replaced:
    "Un déploiement ultérieur a remplacé la cible et terminé l’ancienne fenêtre.",
  notFound: "Le conteneur lié n’existe plus.",
  exited: "Le conteneur s’est arrêté ou a été tué par OOM.",
  restarted: "Un redémarrage a été observé.",
  versionMatched: "L’empreinte attendue du dépôt correspond.",
  oldVersion:
    "Une autre version a été confirmée après le délai de préparation.",
  restartStable: "Aucune augmentation des redémarrages observée.",
  running: "Le conteneur fonctionnait lors de cet échantillon.",
  healthGrace: "Le délai de grâce de santé est encore actif.",
  healthy: "Docker indique un état sain.",
  unhealthy: "Docker indique un état défaillant.",
  healthUnknown: "La santé Docker ne peut pas être établie.",
  healthAbsent:
    "Aucun contrôle de santé Docker ; la santé métier n’est pas couverte.",
  collectionUnavailable:
    "Aucun échantillon valide collecté. Cela seul ne prouve pas une panne applicative.",
  truncated: "Le résultat collecté a été tronqué.",
  deadline: "La limite d’observation est atteinte.",
  userStopped: "L’utilisateur a demandé l’arrêt.",
  interrupted: "Le service a interrompu l’observation.",
  persistenceError: "Les preuves n’ont pas pu être enregistrées.",
  runtimeSample: "Échantillon structuré de fonctionnement",
  baselineSample: "Échantillon juste avant déploiement",
  durationChanged: "La durée a changé.",
  logChanged: "Le périmètre des journaux a changé.",
  targetsChanged: "Le périmètre des cibles a changé.",
  environmentChanged: "L’environnement a changé.",
  rolledBack:
    "Le déploiement a été annulé ; la nouvelle version attendue n’est pas établie.",
  outcomeUnknown: "Le résultat du déploiement est inconnu.",
  deploymentFailed: "Le déploiement a échoué.",
  pendingActivation: "L’identité réelle sera liée après activation.",
};
