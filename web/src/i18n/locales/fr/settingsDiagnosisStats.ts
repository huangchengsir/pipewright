export default {
  title: 'Boucle de retour de diagnostic',
  subtitle:
    'Indicateurs de qualité du diagnostic AI — plus il y a de retours, plus le diagnostic est précis, et les cas erronés deviennent des graines pour la base de connaissances',
  loading: 'Chargement…',
  retry: 'Réessayer',

  emptyTitle: 'Aucun retour de diagnostic pour l’instant',
  emptyHint: 'Appuyez sur 👍/👎 dans le panneau de diagnostic AI d’une exécution échouée et les statistiques seront agrégées ici',

  accuracy: 'Précision',
  accuracyAria: 'Précision {pct}%',

  countTotal: 'Total des retours',
  countUp: '👍 Utile',
  countDown: '👎 À améliorer',

  trendTitle: 'Tendance récente',
  trendBarTitle: '{pct}% · {count} éléments',
  countUnit: '{count} éléments',

  correctionsTitle: 'Corrections récentes (graines de la base de connaissances)',
  correctionsEmpty: 'Aucune cause racine correcte associée à un 👎 pour l’instant.',
  runLabel: 'Exécution {id}',

  errLoadFailed: 'Échec du chargement ({status})',
  errLoadGeneric: 'Échec du chargement des statistiques de diagnostic. Veuillez réessayer plus tard.',
}
