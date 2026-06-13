export default {
  title: 'Applications OAuth',
  sectionDesc:
    'Enregistrez une application OAuth (Client ID / Secret) pour chaque plateforme git afin que les utilisateurs puissent « Connecter » leur compte en un clic depuis le coffre-fort d’identifiants et obtenir automatiquement un jeton, sans coller manuellement un PAT. Le secret est masqué dès son enregistrement et ne peut plus être lu après son écriture.',
  retry: 'Réessayer',

  providerCustomLabel: 'Auto-hébergé',
  providerGiteeDesc: 'Gitee',
  providerCustomDesc: 'GitLab / Gitea auto-hébergé, etc.',

  statusEnabled: 'Activé',
  statusConfiguredIdle: 'Configuré · Non activé',
  statusUnconfigured: 'Non configuré',

  clientIdPlaceholder: 'Obtenez-le sur la page de l’application OAuth de la plateforme git',
  secretOptional: '(seul le masque est affiché après écriture)',
  secretPlaceholderKeep: 'Laissez vide pour conserver le secret existant',
  secretPlaceholderNew: 'Collez le Client Secret…',
  secretStored: 'Enregistré : {masked}',

  toggleAria: 'Activer OAuth {provider}',
  toggleTitle: 'Activer la connexion',
  toggleDesc: 'Une fois activé, le bouton « Connecter » de cette plateforme apparaît dans le coffre-fort d’identifiants',

  lastSaved: 'Dernier enregistrement {time}',
  saveBtn: 'Enregistrer',

  errNoServer: 'Impossible de joindre le serveur. Vérifiez que le backend est en cours d’exécution puis réessayez',
  errVaultUnconfigured: 'Le coffre-fort n’a pas de master key configurée. Définissez la variable d’environnement PIPEWRIGHT_MASTER_KEY',
  errLoadStatus: 'Échec du chargement ({status})',
  errLoadGeneric: 'Échec du chargement des applications OAuth. Veuillez réessayer plus tard',

  errClientIdRequiredEnabled: 'Le Client ID est requis lorsque c’est activé',
  errSecretRequiredEnabled: 'Un Client Secret est requis lorsque c’est activé',
  errBaseUrlRequiredCustom: 'Une instance auto-hébergée nécessite une Base URL',
  errClientIdRequired: 'Veuillez saisir le Client ID',
  errBaseUrlRequired: 'Veuillez saisir la Base URL',

  toastSaveFailed: 'Échec de l’enregistrement',
  toastSaved: 'Application OAuth enregistrée',
  errNoServerShort: 'Impossible de joindre le serveur',
  unknownError: 'Erreur inconnue',

  justNow: 'à l’instant',
  minutesAgo: 'il y a {n} minutes',
  hoursAgo: 'il y a {n} heures',
  daysAgo: 'il y a {n} jours',
}
