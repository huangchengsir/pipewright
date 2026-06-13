export default {
  title: 'Métriques DORA',
  subtitle:
    'Vue de la performance de livraison agrégée à partir des données d’exécution existantes · Fréquence de déploiement / Délai de mise en œuvre / Taux d’échec des changements / Temps de rétablissement',
  generatedAt: '· Données arrêtées au {time}',

  window7d: '7 derniers jours',
  window30d: '30 derniers jours',
  window90d: '90 derniers jours',

  projectLabel: 'Projet',
  projectFilterAria: 'Filtrer par projet',
  allProjects: 'Tous les projets',
  windowAria: 'Fenêtre temporelle',

  errTitle: 'Échec du chargement des métriques DORA',
  errOffline: 'Impossible de joindre le serveur. Vérifiez que le backend est en cours d’exécution, puis réessayez',
  errLoadStatus: 'Échec du chargement des métriques DORA ({status})',
  errLoadRetry: 'Échec du chargement des métriques DORA. Veuillez réessayer plus tard',

  summaryDeployments: 'Déploiements sur {days} jours',
  summarySuccess: 'Réussis',
  summaryFailed: 'Échoués',

  metricDeployFreq: 'Fréquence de déploiement',
  metricLeadTime: 'Délai de mise en œuvre',
  metricCfr: 'Taux d’échec des changements',
  metricMttr: 'Temps moyen de rétablissement',

  capDeployFreq: '{count} déploiements réussis sur {days} jours',
  capLeadTime: 'Durée médiane commit→production sur {count} déploiements réussis',
  capLeadTimeEmpty: 'Aucun déploiement réussi pour l’instant pour calculer le délai',
  capCfr: '{failed} / {total} déploiements en échec',
  capMttr: 'Durée médiane sur {count} paires « échec→rétablissement »',
  capMttrEmpty: 'Aucune paire « échec→rétablissement » dans cette fenêtre',

  noteLead:
    'Méthodologie : un « déploiement » = une exécution atteignant un état terminal ; en l’absence d’horodatage du commit, le délai est approximé par l’heure de mise en file. Les métriques DORA dérivées des données d’exécution CI sont une ',
  noteEmphasis: 'approximation',
  noteTrail: ' à titre indicatif, et non une base de SLA.',
}
