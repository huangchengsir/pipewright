export default {
  // ─── États du conteneur ───
  stateRunning: 'En cours',
  statePaused: 'En pause',
  stateRestarting: 'Redémarrage',
  stateCreated: 'Créé',
  stateExited: 'Arrêté',
  stateDead: 'Anormal',
  stateUnknown: 'Inconnu',

  // ─── Boutons d’action du cycle de vie du conteneur ───
  actionStart: 'Démarrer',
  actionRestart: 'Redémarrer',
  actionStop: 'Arrêter',
  actionPause: 'Mettre en pause',
  actionUnpause: 'Reprendre',
  actionKill: 'Kill',
  actionRm: 'Supprimer',

  // ─── Info-bulles au survol des boutons d’action ───
  hintStart: 'Démarre un conteneur arrêté (docker start), en lançant un nouveau processus depuis le début.',
  hintRestart: 'Redémarrer : arrête d’abord proprement (SIGTERM, 10 s de délai) puis démarre (docker restart).',
  hintStop: 'Arrêt propre : envoie SIGTERM puis, s’il ne se termine pas en 10 s, ajoute SIGKILL (docker stop). À utiliser pour arrêter les services au quotidien, afin que le programme puisse finaliser et écrire sur disque.',
  hintPause: 'Mettre en pause : gèle via cgroup tous les processus du conteneur (docker pause) ; la mémoire est conservée telle quelle et le CPU ne lui est plus alloué. Cliquez sur « Reprendre » pour continuer là où il s’était arrêté. Ne libère pas la mémoire.',
  hintUnpause: 'Reprendre : dégèle un conteneur en pause (docker unpause) ; le même processus reprend là où il s’était arrêté.',
  hintKill: 'Kill forcé : envoie directement SIGKILL pour terminer immédiatement sans possibilité de nettoyage (docker kill) ; des données non écrites peuvent être perdues. À n’utiliser que lorsque « Arrêter » est bloqué.',
  hintRm: 'Supprime le conteneur (docker rm) ; un conteneur en cours doit d’abord être arrêté. La configuration du conteneur est supprimée, mais les volumes de données montés ne sont pas affectés.',

  // ─── Confirmation des actions destructrices ───
  dangerRestartTitle: 'Redémarrer le conteneur {n} ?',
  dangerRestartBody: 'Le conteneur sera arrêté puis redémarré ; son service sera brièvement indisponible pendant ce temps.',
  dangerRestartConfirm: 'Confirmer le redémarrage',
  dangerStopTitle: 'Arrêter le conteneur {n} ?',
  dangerStopBody: 'Le conteneur sera arrêté, interrompant le service qu’il fournit jusqu’à son redémarrage.',
  dangerStopConfirm: 'Confirmer l’arrêt',
  dangerKillTitle: 'Forcer le Kill du conteneur {n} ?',
  dangerKillBody: 'SIGKILL sera envoyé pour terminer immédiatement le processus du conteneur ; des données non écrites peuvent être perdues.',
  dangerKillConfirm: 'Kill forcé',
  dangerRmTitle: 'Supprimer le conteneur {n} ?',
  dangerRmBody: 'Le conteneur sera supprimé (un conteneur en cours doit d’abord être arrêté). Sa configuration est supprimée avec lui ; les volumes de données ne sont pas affectés.',
  dangerRmConfirm: 'Confirmer la suppression',

  // ─── Types de paramètre ───
  paramTypeString: 'Texte',
  paramTypeChoice: 'Énumération',
  paramTypeBoolean: 'Booléen',
  paramTypeNumber: 'Nombre',

  // ─── Validation des valeurs de paramètre ───
  paramRequired: 'Le paramètre « {label} » est obligatoire',
  paramNotNumber: 'Le paramètre « {label} » doit être un nombre',
  paramNotBoolean: 'Le paramètre « {label} » doit être true/false',
  paramNotInChoice: 'Le paramètre « {label} » ne fait pas partie des options disponibles',

  // ─── Statut de promotion ───
  promotionPromoted: 'Promu',
  promotionPending: 'En attente d’approbation',
  promotionRejected: 'Rejeté',

  // ─── Validation du nom d’environnement ───
  envNameEmpty: 'Le nom de l’environnement ne peut pas être vide',
  envNameInvalid: 'Le nom de l’environnement ne peut contenir que des lettres, des chiffres, des tirets et des traits de soulignement',
  envNameTooLong: 'Le nom de l’environnement ne peut pas dépasser 64 caractères',

  // ─── Limite de concurrence ───
  concurrencyNotInteger: 'La limite de concurrence doit être un entier',
  concurrencyTooSmall: 'La limite de concurrence ne peut pas être inférieure à {min}',
  concurrencyTooLarge: 'La limite de concurrence ne peut pas dépasser {max}',
  concurrencyUnlimited: 'Illimité',

  // ─── Terminal ───
  terminalSessionEnded: 'La session de terminal est terminée',
}
