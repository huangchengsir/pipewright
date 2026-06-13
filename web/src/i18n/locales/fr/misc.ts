export default {
  // ─── shared ──────────────────────────────────────────────────────────────
  loadingShort: 'Chargement…',

  // ─── AuditTimeline ───────────────────────────────────────────────────────
  audit: {
    title: "Journal d'audit",
    sub: 'Qui · Quand · Sur quoi',
    treeAria: "Chronologie d'audit",
    emptyLabel: "Aucun enregistrement d'audit pour le moment",
    emptyHint: "Les opérations sensibles telles que la création, la modification ou la suppression d'identifiants et de projets, la réinitialisation des secrets de webhook et le déclenchement manuel d'exécutions sont toutes consignées ici. Les enregistrements sont infalsifiables.",
    loadMore: "Charger plus d'enregistrements d'audit →",
    via: 'Console web',
    actorYou: 'Vous',
    verbCreate: 'A créé',
    verbUpdate: 'A modifié',
    verbDelete: 'A supprimé',
    verbReset: 'A réinitialisé',
    verbAdd: 'A connecté',
    verbTrigger: 'A déclenché manuellement',
    verbDefault: 'A agi sur',
    nounCredential: "l'identifiant",
    nounWebhookSecret: 'le secret de signature de webhook',
    nounProject: 'le projet',
    nounRun: "l'exécution",
    errConnect: "Impossible de se connecter au serveur. Vérifiez que le backend est en cours d'exécution puis réessayez",
    errLoad: "Échec du chargement du journal d'audit ({status})",
    errLoadRetry: "Échec du chargement du journal d'audit. Veuillez réessayer plus tard",
  },

  // ─── CodeTree / CodeTreeNode ─────────────────────────────────────────────
  tree: {
    aria: 'Arborescence des répertoires de code',
    fileAria: 'Arborescence des fichiers du dépôt',
    title: 'Fichiers',
    refTitle: 'Ref actuelle : {ref}',
    loadingDir: 'Chargement du répertoire…',
    emptyRepo: 'Dépôt vide / source illisible',
    emptyDir: 'Répertoire vide',
    errConnect: 'Impossible de se connecter au serveur',
    errNotFound: "Le chemin n'existe pas",
    errLoad: 'Échec du chargement ({status})',
    errLoadGeneric: 'Échec du chargement',
  },

  // ─── CodeViewer ──────────────────────────────────────────────────────────
  code: {
    viewAria: 'Vue du code',
    editorAria: 'Éditeur de code (lecture seule)',
    noFileSelected: 'Aucun fichier sélectionné',
    truncated: 'Tronqué',
    truncatedTitle: 'Fichier trop volumineux ; seule la partie initiale est affichée',
    idleTitle: 'Sélectionnez un fichier à gauche pour le consulter',
    idleSub: 'Parcours en lecture seule du code source du dépôt avec coloration syntaxique ; édition et validation impossibles.',
    binaryTitle: 'Fichier binaire, impossible à prévisualiser',
    degradedTitle: 'Source illisible',
    degradedSub: "Le clonage du dépôt a échoué ou l'environnement actuel ne peut pas y accéder. Veuillez réessayer plus tard ou vérifier la configuration du dépôt du projet.",
    errTitle: 'Échec du chargement du fichier',
    fallbackRegionAria: 'Contenu du code (repli en texte brut)',
    fallbackNote: "Le composant de coloration syntaxique n'a pas pu se charger ; bascule vers une vue en texte brut.",
    errConnect: 'Impossible de se connecter au serveur',
    errNotFound: "Le fichier n'existe pas",
    errLoad: 'Échec du chargement ({status})',
    errLoadGeneric: 'Échec du chargement',
  },

  // ─── ConfirmDialog ───────────────────────────────────────────────────────
  confirm: {
    cancel: 'Annuler',
    confirm: 'Confirmer',
    typeLabelPrefix: 'Saisissez',
    typeLabelSuffix: 'pour confirmer',
    typePlaceholder: 'Saisissez {text}…',
    typeAria: "Saisissez {text} pour confirmer l'action",
  },

  // ─── EmptyState ──────────────────────────────────────────────────────────
  empty: {
    defaultTitle: 'Aucune donnée',
  },

  // ─── ErrorState ──────────────────────────────────────────────────────────
  error: {
    defaultTitle: 'Échec du chargement',
    retry: 'Réessayer',
    aiUnavailableAria: 'Fonction IA indisponible',
    aiTitle: 'Diagnostic des échecs par IA',
    aiTag: 'Indisponible',
    aiDesc: "Le fournisseur LLM n'a pas répondu, aucun diagnostic n'a donc été généré cette fois. Les résultats d'exécution et les journaux sont enregistrés normalement ; le CI/CD principal n'est pas affecté.",
    confidenceLabel: 'Confiance {n}% · {level}',
    confidenceHigh: 'Élevée',
    confidenceMedium: 'Moyenne',
    confidenceLow: 'Faible',
  },

  // ─── ToastHost ───────────────────────────────────────────────────────────
  toast: {
    hostAria: 'Notifications',
    itemAria: 'Notification {type} : {title}',
    closeAria: 'Fermer la notification : {title}',
  },
}
