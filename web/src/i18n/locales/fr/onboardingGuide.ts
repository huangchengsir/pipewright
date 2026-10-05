export default {
  "title": "Guide du pipeline",
  "locate": "Localiser le contrôle",
  "review": "Configuration saisie, continuer",
  "refresh": "Vérifier à nouveau",
  "close": "Quitter le guide",
  "unsaved": "Le brouillon est non enregistré. Quitter le guide conserve les modifications.",
  "phases": {
    "loading": {
      "title": "Vérification de la configuration",
      "description": "Lecture de cette instance uniquement."
    },
    "unknown": {
      "title": "État non confirmé",
      "description": "Réessayez. Un état inconnu ne signifie pas prêt."
    },
    "repository": {
      "title": "Vérifier le dépôt",
      "description": "Ce projet utilise .pipewright.yml. Aucun enregistrement UI distinct ni récupération automatique."
    },
    "stage": {
      "title": "Ajouter une étape",
      "description": "Ajoutez une étape dans le canevas. La source doit être accompagnée d’une tâche réelle."
    },
    "task": {
      "title": "Ajouter la première tâche",
      "description": "Cliquez sur le nœud séquentiel sous l’étape pour ouvrir le sélecteur."
    },
    "choose": {
      "title": "Choisir un type de tâche",
      "description": "Choisissez un type adapté. Un script personnalisé convient pour commencer ; saisissez son image et ses commandes."
    },
    "configure": {
      "title": "Configurer la tâche",
      "description": "Ouvrez la tâche et renseignez les champs nécessaires à droite. Un script nécessite image et commandes. Continuez ensuite."
    },
    "save": {
      "title": "Enregistrer et vérifier",
      "description": "Enregistrez le brouillon dans l’en-tête. Les prérequis seront vérifiés ; saisir les champs ne suffit pas."
    },
    "issues": {
      "title": "Enregistré, prérequis manquants",
      "description": "Ouvrez les éléments pour les configurer. Si aucune tâche ne correspond à la branche, vérifiez les conditions puis enregistrez."
    },
    "runtime": {
      "title": "Préparer l’environnement",
      "description": "La configuration est enregistrée mais l’exécution n’est pas prête. Réenregistrer ne résout pas un exécuteur de démonstration."
    },
    "ready": {
      "title": "Prêt à confirmer une exécution",
      "description": "Configuration, références et exécuteur vérifiés. Seule la confirmation s’ouvre ; les connexions ne sont pas prouvées."
    }
  }
}
