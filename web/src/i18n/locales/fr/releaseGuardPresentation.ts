export default {
  aiTitle: 'Interprétation par IA',
  boundaryDetails: 'Limites du rapport et conservation des preuves',
  titles: {
    notFound: 'Absence du conteneur confirmée lors du prélèvement',
    unknown: 'Cette preuve ne peut pas encore être interprétée',
    unavailable: 'Aucun échantillon valide obtenu',
    running: 'Conteneur en cours d’exécution lors du prélèvement',
    exited: 'Conteneur arrêté lors du prélèvement',
    oom: 'Arrêt par manque de mémoire enregistré',
    image: 'Identité de l’image attendue enregistrée',
    gateFailed: 'Échec du contrôle de santé au déploiement',
    logs: 'Extraits bornés des journaux conservés',
    unhealthy: 'Le contrôle de santé Docker a échoué lors du prélèvement',
    logsUnavailable: 'Aucun journal de la fenêtre obtenu',
    logsPartial: 'Seule une partie des journaux est conservée',
    logsEmpty: 'Fenêtre de journaux collectée ; aucune ligne reçue',
  },
  logExcerpt:
    'Extrait des journaux (600 caractères maximum ; contenu complet dans les détails techniques)',
  snapshotScope:
    'Interprète uniquement l’instantané choisi et autorisé pour chaque envoi. N’analyse pas tout le rapport et ne lance pas d’enquête automatique.',
  nextTitle: 'Prochaines étapes',
  nextDescription:
    'Consultez les preuves avant d’enquêter ou de répéter l’observation.',
  investigateAction: 'Examiner les changements associés',
  why: 'Pourquoi la vérification a échoué',
  failedFallback:
    'Les règles confirment un échec. Consultez les données techniques pour les détails disponibles.',
  unknownTitle: 'Éléments non confirmés',
  unknownScope:
    'Les échantillons ne prouvent ni la disponibilité continue ni la santé métier. La cause complète exige des preuves indépendantes.',
  unconfirmed:
    'Information non confirmée ; le relevé original reste dans les détails techniques.',
  unknown: 'Inconnu',
  unknownTarget: 'Cible non confirmée',
  unknownCondition: 'Condition inconnue',
  reportMissing: 'Ce rapport est introuvable.',
  technicalDetails: 'Détails techniques',
  reportDetails: 'Données originales du rapport',
  evidenceReference: 'Voir la preuve {n}',
  evidenceLoaded: '{n} chargées ; seules les preuves chargées sont comptées.',
  continueFinding: 'Poursuivre la recherche de cette preuve',
  find: {
    found: 'La preuve citée a été trouvée.',
    pending: 'La preuve n’est pas chargée. Continuez page par page.',
    loading: 'Lecture et recherche des preuves…',
    missing: 'La dernière page est atteinte. Cette preuve reste introuvable.',
    expired: 'Les preuves ont expiré. La conclusion historique est conservée.',
    error: 'Lecture impossible. Poursuivez la recherche manuellement.',
    stalled:
      'Le curseur n’a pas avancé. La recherche s’est arrêtée ; actualisez le rapport.',
  },
  kinds: {
    runtime: 'Échantillon d’exécution',
    baseline: 'Référence avant déploiement',
    expected_image: 'Relevé de l’image attendue',
    deployment_health_gate: 'Contrôle de santé au déploiement',
    logs: 'Journaux de la fenêtre',
    unknown: 'Type de preuve inconnu',
  },
  running: 'État d’exécution',
  runningValue: 'En cours d’exécution',
  stoppedValue: 'Arrêté',
  exitCode: 'Code de sortie',
  oom: 'Arrêt par manque de mémoire',
  restarts: 'Nombre de redémarrages',
  yes: 'Oui',
  no: 'Non',
  health: 'État de santé Docker',
  healthValues: {
    healthy: 'Contrôle Docker réussi',
    unhealthy: 'Échec du contrôle Docker',
    starting: 'Contrôle Docker en démarrage',
    absent: 'Non configuré ; santé métier inconnue',
    unknown: 'Inconnu',
  },
  imageIdentity: 'Identité de l’image',
  imageRecorded: 'Valeur d’identité',
  recorded: 'Enregistrée ; voir les détails techniques',
  identityValues: {
    config_id: 'Identifiant local d’image Docker',
    manifest: 'Empreinte du manifeste',
    index: 'Empreinte de l’index',
    repository_digest_unknown: 'Type d’empreinte inconnu',
    unknown: 'Inconnu',
  },
  gate: 'Contrôle du déploiement',
  failed: 'Échec',
  windowStart: 'Début des journaux',
  windowEnd: 'Fin des journaux',
  logGap: 'Lacune dans la planification de collecte',
  fileStatus: {
    added: 'Ajouté',
    modified: 'Modifié',
    deleted: 'Supprimé',
    renamed: 'Renommé',
    copied: 'Copié',
    unknown: 'État inconnu',
  },
  timeline: {
    deploymentStarted: 'Tentative de déploiement démarrée',
    activationStarted: 'Activation du conteneur démarrée',
    deploymentFinished: 'Tentative de déploiement terminée',
    baselineFailing:
      'La référence préalable signalait déjà un échec d’exécution',
    logsRequested: 'Collecte bornée des journaux demandée',
    logsCollected: 'Résultat de collecte des journaux enregistré',
    logsUnavailable: 'Journaux de la fenêtre indisponibles',
    finished: 'Tâche d’observation terminée',
    investigated: 'Enquête sur les changements mise à jour',
    evidenceExpired: 'Preuves originales expirées',
  },
  runtimeImageTruncated:
    'Les métadonnées de l’image ont été tronquées. Les faits d’exécution restent disponibles ; l’identité complète de l’image n’est pas confirmée.',
  targetBindingChanged:
    'La configuration a changé après la collecte. Ces faits ne confirment pas la validité actuelle du rattachement de la cible.',
  targetBindingUnknown:
    'L’indicateur de changement de cible est invalide. Son rattachement actuel n’est pas confirmé.',
  logsPartialScope:
    'Seul l’extrait conservé est disponible. La fenêtre demandée peut contenir des éléments omis ou tronqués.',
  logLines: 'Lignes collectées',
  reasons: {
    sourceCacheBusy:
      'Le cache local est occupé ; la comparaison du code n’a pas été obtenue.',
    sourceCacheUnreadable:
      'Le cache local est illisible ; la comparaison n’est pas confirmée.',
    baseCommitNotCached:
      'Le commit de référence n’est pas en cache ; la comparaison exacte est indisponible.',
    currentCommitNotCached:
      'Le commit actuel n’est pas en cache ; la comparaison exacte est indisponible.',
    logTimeGap:
      'La collecte présente une lacune temporelle. Les périodes manquantes ne prouvent pas un fonctionnement normal.',
    serviceRestarted:
      'Un redémarrage du service a interrompu l’observation. Cela ne prouve pas une défaillance de l’application.',
    resolvedSourceUnavailable:
      'Aucun commit source complet et vérifiable n’est disponible ; la comparaison exacte est impossible.',
    sourceRunUnavailable:
      'L’exécution source est indisponible ou non vérifiée ; son commit ne peut pas être confirmé.',
    sourceRevisionConflict:
      'Les relevés du commit source sont en conflit et ne permettent pas une comparaison exacte.',
    sourceRevisionNotConfirmed:
      'Les relevés d’exécution et de déploiement ne confirment pas le même commit source ; la comparaison est indisponible.',
    currentDeploymentIdentityUnavailable:
      'Le relevé d’identité du déploiement actuel est indisponible ; aucune référence de comparaison fiable ne peut être vérifiée.',
    deploymentHistoryUnavailable:
      'L’historique des déploiements est indisponible ; la référence de comparaison des changements ne peut pas être vérifiée.',
  },
  citationLabel: '{candidate} · {context} · Preuve {n}',
}
