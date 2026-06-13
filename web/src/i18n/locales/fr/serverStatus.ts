export default {
  title: 'État des serveurs',
  subtitle: "Charge CPU, mémoire et utilisation du disque de tous les serveurs enregistrés, collectées en temps réel via SSH",
  reachableSummary: '{reachable}/{total} joignables',
  autoRefresh: 'Actualisation automatique toutes les {n} s',
  loadingAria: "Chargement de l'état des serveurs",
  errTitle: "Échec du chargement de l'état des serveurs",
  errConnect: "Impossible de se connecter au serveur. Vérifiez que le backend est en cours d'exécution, puis réessayez.",
  errLoadStatus: "Échec du chargement de l'état des serveurs ({status})",
  errLoadRetry: "Échec du chargement de l'état des serveurs. Veuillez réessayer plus tard.",
  emptyTitle: 'Aucun serveur enregistré pour le moment',
  emptyDesc: 'Enregistrez des serveurs cibles dans « Paramètres › Serveurs » et leurs métriques de ressources apparaîtront ici.',
}
