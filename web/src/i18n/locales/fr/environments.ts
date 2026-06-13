export default {
  title: 'Environnements',
  subtitle:
    "Historique des déploiements regroupé par environnement et version active actuelle · revenez au dernier déploiement réussi en un clic",
  project: 'Projet',
  filterByProject: 'Filtrer par projet',
  selectProject: 'Sélectionnez un projet',

  emptySelectTitle: 'Sélectionnez un projet',
  emptySelectDesc: "L'historique des déploiements est regroupé par projet : choisissez-en un ci-dessus d'abord.",
  emptyTitle: "Aucun historique de déploiement pour ce projet pour l'instant",
  emptyDesc:
    "Une fois qu'un déploiement a été exécuté sur un environnement (le mappage de branche du webhook résout le nom de l'environnement et le déploiement se termine), la chronologie apparaîtra ici, regroupée par environnement.",

  errLoadTitle: "Échec du chargement de l'historique des déploiements",
  errNetwork: "Impossible de joindre le serveur. Vérifiez que le backend est en cours d'exécution et réessayez.",
  errLoad: "Échec du chargement de l'historique des déploiements ({status})",
  errLoadRetry: "Échec du chargement de l'historique des déploiements. Veuillez réessayer plus tard.",

  active: 'Active',
  activeVersionTitle: 'Version active actuelle',
  noActiveVersion: 'Aucune version active',
  noFullSuccessTitle: "Aucun déploiement entièrement réussi pour l'instant",
  targetCount: '{n} hôtes cibles',

  rollback: 'Annuler',
  rollbackEnabledTitle: 'Revenir au dernier déploiement réussi',
  rollbackDisabledTitle: 'Aucun déploiement réussi précédent vers lequel revenir',
  rollbackTitle: "Annuler l'environnement « {env} »",
  rollbackBody:
    "Cela ramène l'environnement au dernier déploiement réussi (exécution {commit} · {when}), en redéployant ces artefacts sur les hôtes cibles d'origine. Cette action déclenche un déploiement réel.",
  rollbackConfirm: "Confirmer l'annulation",
  rollbackFailedStatus: "Échec de l'annulation ({status})",
  rollbackFailedRetry: "Échec de l'annulation. Veuillez réessayer plus tard.",

  toastRolledBack: 'Environnement « {env} » annulé',
  toastRolledBackDetail: 'Artefacts redéployés sur {n} hôtes cibles',
  toastRollbackPartial: "L'annulation de l'environnement « {env} » a partiellement échoué",
  toastRollbackPartialDetail: '{failed}/{total} hôtes cibles en échec',
  toastRollbackFailed: "Échec de l'annulation de l'environnement « {env} »",

  timelineAria: 'Historique des déploiements de {env}',
}
