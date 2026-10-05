export default {
  "repairCredentialDescription": "Choisissez à nouveau les identifiants de ce projet. Il est mis à jour uniquement à l'enregistrement.",
  "title": "Première exécution réussie",
  "intro": "Créez un projet, préparez son pipeline, puis confirmez vous-même l'exécution.",
  "projectLabel": "Projet actuel",
  "resultProjectLabel": "Projet de l'exécution réussie",
  "projectUnavailable": "Nom du projet indisponible",
  "stepsLabel": "Étapes de configuration",
  "steps": {
    "project": "Créer un projet",
    "pipeline": "Préparer le pipeline",
    "run": "Réussir une exécution"
  },
  "stepDescriptions": {
    "project": "Connectez votre dépôt. La création d'un projet nécessite actuellement des identifiants du dépôt.",
    "pipeline": "Enregistrez la configuration applicable et vérifiez les identifiants et l'environnement nécessaires aux tâches réelles. Les serveurs sans rapport ne sont pas requis.",
    "run": "Envoyez la tâche via le dialogue de confirmation existant, puis consultez le résultat."
  },
  "stepStates": {
    "saved": "Enregistré",
    "done": "Terminé",
    "current": "Étape actuelle",
    "pending": "À venir",
    "unknown": "Non confirmé"
  },
  "states": {
    "loading": "Lecture de l'état de l'instance",
    "unknown": "Confirmation impossible pour le moment",
    "create": "Créez d'abord un projet",
    "queued": "Exécution en attente",
    "running": "Pipeline en cours",
    "waiting_approval": "En attente d'approbation",
    "failed": "Consultez le dernier échec",
    "configure": "Poursuivez la préparation du pipeline",
    "unconfirmed": "Vérifiez la configuration du pipeline",
    "repository": "Vérifiez le pipeline du dépôt",
    "ready": "Vous pouvez ouvrir la confirmation",
    "success": "Exécution du pipeline réussie",
    "legacy": "Un enregistrement de réussite existe",
    "stub": "La démonstration ne termine pas le guide",
    "mixed": "L'exécution mixte ne termine pas le guide",
    "pending": "Preuve d'exécution non confirmée"
  },
  "descriptions": {
    "loading": "Seules les données de cette instance sont lues. Vous pouvez ignorer le guide ou revenir au tableau de bord.",
    "unknown": "L'état complet n'a pas pu être confirmé. Cela ne signifie pas qu'il n'y a ni projet ni réussite. Réessayez.",
    "create": "Utilisez le formulaire existant, choisissez les identifiants et indiquez la branche par défaut.",
    "queued": "Consultez la dernière tâche du projet sélectionné. Aucun nouvel envoi n'est nécessaire.",
    "running": "Consultez la progression et le résultat de la dernière exécution.",
    "waiting_approval": "Consultez l'approbation dans les détails. Le guide n'approuve rien automatiquement.",
    "failed": "Consultez les journaux de cette exécution, puis corrigez le même projet avant de relancer.",
    "configure": "Vérifiez les éléments requis ci-dessous. Enregistrer un serveur ne vérifie pas sa connexion.",
    "unconfirmed": "La disponibilité de la configuration n'est pas confirmée. Vérifiez-la ou utilisez l'entrée d'exécution manuelle existante.",
    "repository": "Ce projet utilise .pipewright.yml dans le dépôt. Le guide ne récupère pas le dépôt et n'exige pas une configuration d'interface distincte.",
    "ready": "Ouvrez la confirmation de ce projet. L'exécution ne commence qu'après votre envoi.",
    "success": "L'instance possède une réussite avec une preuve d'exécution réelle. Cela ne prouve pas en plus un déploiement ou la connectivité du service.",
    "legacy": "La réussite historique satisfait les règles de compatibilité. Elle ne prouve pas en plus une compilation ou un déploiement réels.",
    "stub": "Cette réussite inclut une démonstration et ne prouve pas le fonctionnement des tâches réelles.",
    "mixed": "Cette exécution mêle tâches réelles et démonstrations ; elle ne compte pas comme première réussite réelle.",
    "pending": "L'exécution a réussi, mais sa preuve reste non confirmée. Une réussite réelle n'est pas déduite."
  },
  "actions": {
    "create": "Créer un projet",
    "configure": "Continuer la configuration",
    "checkPipeline": "Vérifier le pipeline",
    "goRun": "Aller à l'exécution",
    "viewRun": "Voir l'exécution",
    "viewFailed": "Voir les journaux d'échec",
    "edit": "Modifier le pipeline",
    "viewResult": "Voir le résultat",
    "autoTrigger": "Configurer les déclencheurs automatiques",
    "runtimeHelp": "Aide de l'environnement d'exécution",
    "retry": "Réessayer",
    "open": "Ouvrir cette étape",
    "servers": "Enregistrer un serveur",
    "repairCredential": "Changer l'identifiant du dépôt",
    "credentials": "Gérer les identifiants"
  },
  "issues": {
    "build": "Vérifiez la configuration et les outils nécessaires à cette compilation.",
    "tasks": "Vérifiez les types de tâches, images et commandes. Cet exécuteur peut ne pas prendre en charge certaines tâches.",
    "credentials": "Une référence d'identifiant utilisée par les tâches réelles manque.",
    "projectCredential": "L'identifiant du dépôt manque. Sélectionnez-le à nouveau dans le projet.",
    "vault": "Aucune clé maîtresse n'est configurée pour le coffre. L'utilisation des identifiants existants ne peut être confirmée.",
    "environment": "L'environnement référencé par la branche actuelle n'est pas défini.",
    "noTasks": "La branche actuelle ne contient aucune tâche réelle exécutable.",
    "notification": "Le canal référencé par la tâche de notification n'existe pas.",
    "pipeline": "Vérifiez la structure du pipeline.",
    "runtime": "L'exécuteur choisi au démarrage est en mode démonstration.",
    "server": "Un nœud ou exécuteur distant référence un serveur absent.",
    "storage": "La configuration concernée n'a pas pu être lue. Réessayez."
  },
  "runtime": {
    "stub": "Un exécuteur de démonstration est effectivement choisi et ne peut pas exécuter de tâches réelles.",
    "available": "Un exécuteur réel est choisi et la CLI locale de conteneurs a été détectée. Cela ne vérifie pas la connexion au service de conteneurs.",
    "unknown": "La capacité d'exécution ou la connexion distante reste non confirmée. Aucun test de connexion automatique n'est effectué.",
    "instructions": "Vérifiez la CLI, les droits du service et la connexion sur l'hôte ou le conteneur exécutant Pipewright. Les exécuteurs distants se configurent dans l'onglet des variables du projet. Cette aide n'installe rien, ne teste pas SSH et n'envoie aucune tâche."
  }
}
