export default {
  // ─── page header ───────────────────────────────────────────────
  title: 'Bibliothek',
  subtitle: 'Projektübergreifend geteilte Pipeline-Vorlagen, Variablengruppen und benutzerdefinierte Knoten · einmal definieren, überall wiederverwenden',
  newGroup: '+ Neue Variablengruppe',
  newStudioNode: '+ Neuer Studio-Knoten',

  // ─── segmented tabs ────────────────────────────────────────────
  segAria: 'Bibliothekskategorien',
  tabTemplates: 'Pipeline-Vorlagen',
  tabVariableGroups: 'Variablengruppen',
  tabCustomNodes: 'Benutzerdefinierte Knoten',

  // ─── common ────────────────────────────────────────────────────
  retry: 'Erneut versuchen',
  delete: 'Löschen',
  edit: 'Bearbeiten',
  cancel: 'Abbrechen',
  save: 'Speichern',
  saving: 'Wird gespeichert…',
  close: 'Schließen',
  remove: 'Entfernen',
  noDescription: 'Keine Beschreibung',
  emptyValue: 'leer',
  updatedAt: 'Aktualisiert am {time}',

  // ─── templates ─────────────────────────────────────────────────
  emptyTemplatesTitle: 'Noch keine Pipeline-Vorlagen',
  emptyTemplatesHint: 'Mit Vorlagen können Sie eine Pipeline-Definition festhalten und sie mit einem Klick im Pipeline-Editor eines beliebigen Projekts anwenden. Im Pipeline-Editor eines Projekts können Sie die aktuelle Pipeline als Vorlage speichern.',
  stageCount: '{n} Phasen',

  // ─── variable groups ───────────────────────────────────────────
  emptyGroupsTitle: 'Noch keine Variablengruppen',
  emptyGroupsHint: 'Definieren Sie einen Satz gemeinsam genutzter Variablen (etwa dieselben Umgebungsadressen oder Token-Referenzen) als Variablengruppe und verwenden Sie ihn in mehreren Pipelines wieder. Secret-Variablen speichern nur eine Vault-Referenz, niemals den Klartext.',
  varCount: '{n} Variablen',
  secretRefTitle: 'Vault-Referenz, Klartext nicht sichtbar',
  moreVars: '+{n} weitere Variablen…',
  noVars: 'Keine Variablen',

  // ─── custom nodes ──────────────────────────────────────────────
  emptyNodesTitle: 'Noch keine benutzerdefinierten Knoten',
  emptyNodesHint: 'Klicken Sie oben rechts auf „Neuer Studio-Knoten“, um Schritte per Low-Code zu kombinieren und Parameter hochzustufen und so einen wiederverwendbaren Knoten zu erstellen; oder konfigurieren Sie im Pipeline-Editor einen beliebigen Knoten und klicken Sie auf „Als benutzerdefinierten Knoten speichern“. Danach können Sie ihn in jeder Pipeline über die Knotenauswahl mit einem Klick wiederverwenden.',
  moreParams: '+{n} weitere Parameter…',
  noParams: 'Keine Parameter',

  // ─── variable-group editor ─────────────────────────────────────
  createGroup: 'Neue Variablengruppe',
  editGroup: 'Variablengruppe bearbeiten',
  fieldName: 'Name',
  fieldDescriptionOptional: 'Beschreibung (optional)',
  fieldVariables: 'Variablen',
  addVariable: '+ Variable hinzufügen',
  groupNamePlaceholder: 'z. B. prod-shared-env',
  groupDescPlaceholder: 'Wofür diese Variablengruppe dient',
  selectCredential: 'Anmeldedaten auswählen…',
  secretToggleOn: 'Vault-Secret (klicken, um zu Klartext zurückzuwechseln)',
  secretToggleOff: 'Klartext (klicken, um in Secret umzuwandeln)',

  // ─── custom-node editor ────────────────────────────────────────
  editNode: 'Benutzerdefinierten Knoten bearbeiten',
  fieldSummaryOptional: 'Zusammenfassung (optional)',
  fieldUnderlyingType: 'Zugrunde liegender Typ',
  underlyingTypeHint: 'Der zugrunde liegende Job-Typ kann nicht geändert werden',
  fieldParams: 'Parameter',
  addParam: '+ Parameter hinzufügen',
  nodeNamePlaceholder: 'z. B. build-and-push',
  nodeDescPlaceholder: 'Wofür dieser Knoten dient',
  nodeSummaryPlaceholder: 'Einzeilige Zusammenfassung, die auf der Karte angezeigt wird',
  noParamsHint: 'Noch keine Parameter. Klicken Sie auf „+ Parameter hinzufügen“, um einen hinzuzufügen.',

  // ─── confirms / toasts / errors ────────────────────────────────
  confirmDeleteTemplate: 'Vorlage „{name}“ löschen? Diese Aktion kann nicht rückgängig gemacht werden.',
  deletedTemplate: 'Vorlage „{name}“ gelöscht',
  confirmDeleteGroup: 'Variablengruppe „{name}“ löschen? Diese Aktion kann nicht rückgängig gemacht werden.',
  deletedGroup: 'Variablengruppe „{name}“ gelöscht',
  createdGroup: 'Variablengruppe „{name}“ erstellt',
  updatedGroup: 'Variablengruppe „{name}“ aktualisiert',
  confirmDeleteNode: 'Benutzerdefinierten Knoten „{name}“ löschen? Diese Aktion kann nicht rückgängig gemacht werden.',
  deletedNode: 'Benutzerdefinierten Knoten „{name}“ gelöscht',
  updatedNode: 'Benutzerdefinierten Knoten „{name}“ aktualisiert',
  groupNameRequired: 'Der Name der Variablengruppe darf nicht leer sein',
  nodeNameRequired: 'Der Name des benutzerdefinierten Knotens darf nicht leer sein',
  deleteFailed: 'Löschen fehlgeschlagen',
  saveFailed: 'Speichern fehlgeschlagen',
  saveFailedStatus: 'Speichern fehlgeschlagen ({status})',
  loadTemplatesFailed: 'Vorlagen konnten nicht geladen werden',
  loadTemplatesFailedStatus: 'Vorlagen konnten nicht geladen werden ({status})',
  loadGroupsFailed: 'Variablengruppen konnten nicht geladen werden',
  loadGroupsFailedStatus: 'Variablengruppen konnten nicht geladen werden ({status})',
  loadNodesFailed: 'Benutzerdefinierte Knoten konnten nicht geladen werden',
  loadNodesFailedStatus: 'Benutzerdefinierte Knoten konnten nicht geladen werden ({status})',
}
