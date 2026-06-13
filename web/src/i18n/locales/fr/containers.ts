export default {
  title: 'Conteneurs',
  subtitle: 'Gérez les conteneurs et les images par hôte sur tous les serveurs enregistrés',
  countSummary: '· {total} conteneurs au total · {running} en cours d’exécution',
  autoRefresh: '· Actualisation automatique toutes les {n} s',

  aiAssistant: '✦ Assistant IA',
  prune: '🧹 Nettoyer',
  bulkEnter: 'Par lot',
  bulkExit: 'Quitter le mode lot',
  create: '+ Nouveau conteneur',

  loadingAria: 'Chargement de la liste des conteneurs',
  errTitle: 'Échec du chargement de la liste des conteneurs',
  errConnect: 'Impossible de se connecter au serveur. Vérifiez que le backend est en cours d’exécution et réessayez.',
  errLoadStatus: 'Échec du chargement de la liste des conteneurs ({status})',
  errLoadRetry: 'Échec du chargement de la liste des conteneurs. Veuillez réessayer plus tard.',

  emptyTitle: 'Aucun serveur enregistré pour le moment',
  emptyDesc: 'Enregistrez les serveurs cibles dans « Paramètres › Serveurs » et leurs conteneurs et images seront regroupés ici.',

  kpiTotal: 'Total des conteneurs',
  kpiRunning: 'En cours d’exécution',
  kpiStopped: 'Arrêtés',
  kpiHosts: 'Serveurs avec conteneurs',
  kpiStripAria: 'Statistiques agrégées des conteneurs',

  filterAria: 'Filtrer les conteneurs par état',
  filterAll: 'Tous',
  filterRunning: 'En cours d’exécution',
  filterStopped: 'Arrêtés',
  filterPaused: 'En pause',

  searchPlaceholder: 'Rechercher des conteneurs par nom / image',
  searchAria: 'Rechercher des conteneurs par nom ou image',
  searchClear: 'Effacer la recherche',

  bulkAria: 'Actions par lot',
  bulkSelected: 'Sélectionnés',
  bulkSelectedUnit: '',
  bulkClear: 'Effacer la sélection',
  actionStart: 'Démarrer',
  actionStop: 'Arrêter',
  actionRestart: 'Redémarrer',
  actionDelete: 'Supprimer',

  confirmTitle: '{label} par lot {n} conteneurs ?',
  confirmBodyRm: 'Les conteneurs sélectionnés seront supprimés (docker rm). Les conteneurs en cours d’exécution doivent d’abord être arrêtés, sinon la suppression échouera (comptée comme un échec).',
  confirmBodyAction: 'L’action {label} sera exécutée sur les {n} conteneurs sélectionnés ; les services associés peuvent être brièvement interrompus.',
  confirmLabel: '{label} {n}',

  toastDone: '{label} par lot terminé',
  toastDoneDetail: '{n} réussis',
  toastFail: '{label} par lot échoué',
  toastFailDetail: '{n} échoués',
  toastPartial: '{label} par lot partiellement terminé',
  toastPartialDetail: '{ok} réussis · {fail} échoués',

  cardsAria: 'Cartes de conteneurs par serveur',
  aiContextContainer: '(hôte docker)',
}
