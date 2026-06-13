export default {
  // ─── page header ───────────────────────────────────────────────
  title: 'Library',
  subtitle: 'Pipeline templates, variable groups and custom nodes shared across projects · define once, reuse everywhere',
  newGroup: '+ New variable group',
  newStudioNode: '+ New studio node',

  // ─── segmented tabs ────────────────────────────────────────────
  segAria: 'Library categories',
  tabTemplates: 'Pipeline templates',
  tabVariableGroups: 'Variable groups',
  tabCustomNodes: 'Custom nodes',

  // ─── common ────────────────────────────────────────────────────
  retry: 'Retry',
  delete: 'Delete',
  edit: 'Edit',
  cancel: 'Cancel',
  save: 'Save',
  saving: 'Saving…',
  close: 'Close',
  remove: 'Remove',
  noDescription: 'No description',
  emptyValue: 'empty',
  updatedAt: 'Updated {time}',

  // ─── templates ─────────────────────────────────────────────────
  emptyTemplatesTitle: 'No pipeline templates yet',
  emptyTemplatesHint: 'Templates let you capture a pipeline definition once and apply it with one click in any project’s pipeline editor. You can save the current pipeline as a template from a project pipeline editor.',
  stageCount: '{n} stages',

  // ─── variable groups ───────────────────────────────────────────
  emptyGroupsTitle: 'No variable groups yet',
  emptyGroupsHint: 'Define a set of shared variables (such as the same environment addresses or token references) as a variable group and reuse it across multiple pipelines. Secret variables store only a vault reference, never plaintext.',
  varCount: '{n} variables',
  secretRefTitle: 'Vault reference, plaintext not visible',
  moreVars: '+{n} more variables…',
  noVars: 'No variables',

  // ─── custom nodes ──────────────────────────────────────────────
  emptyNodesTitle: 'No custom nodes yet',
  emptyNodesHint: 'Click "New studio node" in the top right to compose steps and promote parameters in a low-code way into a reusable node; or configure any node in the pipeline editor and click "Save as custom node". You can then reuse it with one click from the node picker in any pipeline.',
  moreParams: '+{n} more parameters…',
  noParams: 'No parameters',

  // ─── variable-group editor ─────────────────────────────────────
  createGroup: 'New variable group',
  editGroup: 'Edit variable group',
  fieldName: 'Name',
  fieldDescriptionOptional: 'Description (optional)',
  fieldVariables: 'Variables',
  addVariable: '+ Add variable',
  groupNamePlaceholder: 'e.g. prod-shared-env',
  groupDescPlaceholder: 'What this group of variables is for',
  selectCredential: 'Select credential…',
  secretToggleOn: 'Vault secret (click to switch back to plaintext)',
  secretToggleOff: 'Plaintext (click to switch to secret)',

  // ─── custom-node editor ────────────────────────────────────────
  editNode: 'Edit custom node',
  fieldSummaryOptional: 'Summary (optional)',
  fieldUnderlyingType: 'Underlying type',
  underlyingTypeHint: 'The underlying job type cannot be changed',
  fieldParams: 'Parameters',
  addParam: '+ Add parameter',
  nodeNamePlaceholder: 'e.g. build-and-push',
  nodeDescPlaceholder: 'What this node is for',
  nodeSummaryPlaceholder: 'One-line summary shown on the card',
  noParamsHint: 'No parameters yet. Click "+ Add parameter" to add one.',

  // ─── confirms / toasts / errors ────────────────────────────────
  confirmDeleteTemplate: 'Delete template "{name}"? This action cannot be undone.',
  deletedTemplate: 'Deleted template "{name}"',
  confirmDeleteGroup: 'Delete variable group "{name}"? This action cannot be undone.',
  deletedGroup: 'Deleted variable group "{name}"',
  createdGroup: 'Created variable group "{name}"',
  updatedGroup: 'Updated variable group "{name}"',
  confirmDeleteNode: 'Delete custom node "{name}"? This action cannot be undone.',
  deletedNode: 'Deleted custom node "{name}"',
  updatedNode: 'Updated custom node "{name}"',
  groupNameRequired: 'Variable group name cannot be empty',
  nodeNameRequired: 'Custom node name cannot be empty',
  deleteFailed: 'Delete failed',
  saveFailed: 'Save failed',
  saveFailedStatus: 'Save failed ({status})',
  loadTemplatesFailed: 'Failed to load templates',
  loadTemplatesFailedStatus: 'Failed to load templates ({status})',
  loadGroupsFailed: 'Failed to load variable groups',
  loadGroupsFailedStatus: 'Failed to load variable groups ({status})',
  loadNodesFailed: 'Failed to load custom nodes',
  loadNodesFailedStatus: 'Failed to load custom nodes ({status})',
}
