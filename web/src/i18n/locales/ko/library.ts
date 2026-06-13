export default {
  // ─── page header ───────────────────────────────────────────────
  title: '라이브러리',
  subtitle: '프로젝트 간 공유되는 파이프라인 템플릿, 변수 그룹, 사용자 정의 노드 · 한 번 정의하면 어디서나 재사용',
  newGroup: '+ 변수 그룹 생성',
  newStudioNode: '+ 스튜디오 노드 생성',

  // ─── segmented tabs ────────────────────────────────────────────
  segAria: '라이브러리 분류',
  tabTemplates: '파이프라인 템플릿',
  tabVariableGroups: '변수 그룹',
  tabCustomNodes: '사용자 정의 노드',

  // ─── common ────────────────────────────────────────────────────
  retry: '다시 시도',
  delete: '삭제',
  edit: '편집',
  cancel: '취소',
  save: '저장',
  saving: '저장 중…',
  close: '닫기',
  remove: '제거',
  noDescription: '설명 없음',
  emptyValue: '비어 있음',
  updatedAt: '{time} 업데이트됨',

  // ─── templates ─────────────────────────────────────────────────
  emptyTemplatesTitle: '아직 파이프라인 템플릿이 없습니다',
  emptyTemplatesHint: '템플릿을 사용하면 파이프라인 정의를 저장해 두고 모든 프로젝트의 파이프라인 편집기에서 한 번의 클릭으로 적용할 수 있습니다. 프로젝트 파이프라인 편집기에서 현재 파이프라인을 템플릿으로 저장할 수 있습니다.',
  stageCount: '{n}개 단계',

  // ─── variable groups ───────────────────────────────────────────
  emptyGroupsTitle: '아직 변수 그룹이 없습니다',
  emptyGroupsHint: '공유 변수 집합(예: 동일한 환경 주소, 토큰 참조)을 변수 그룹으로 정의하여 여러 파이프라인 간에 재사용하세요. secret 변수는 vault 참조만 저장하며 평문은 절대 저장하지 않습니다.',
  varCount: '변수 {n}개',
  secretRefTitle: 'vault 참조, 평문은 표시되지 않음',
  moreVars: '변수 {n}개 더…',
  noVars: '변수 없음',

  // ─── custom nodes ──────────────────────────────────────────────
  emptyNodesTitle: '아직 사용자 정의 노드가 없습니다',
  emptyNodesHint: '오른쪽 상단의 "스튜디오 노드 생성"을 클릭하여 로우코드 방식으로 단계를 조합하고 매개변수를 끌어올려 재사용 가능한 노드로 저장하세요. 또는 파이프라인 편집기에서 노드의 매개변수를 설정한 뒤 "사용자 정의 노드로 저장"을 클릭하세요. 이후 모든 파이프라인의 노드 선택기에서 한 번의 클릭으로 재사용할 수 있습니다.',
  moreParams: '매개변수 {n}개 더…',
  noParams: '매개변수 없음',

  // ─── variable-group editor ─────────────────────────────────────
  createGroup: '변수 그룹 생성',
  editGroup: '변수 그룹 편집',
  fieldName: '이름',
  fieldDescriptionOptional: '설명(선택)',
  fieldVariables: '변수',
  addVariable: '+ 변수 추가',
  groupNamePlaceholder: '예: prod-shared-env',
  groupDescPlaceholder: '이 변수 그룹의 용도',
  selectCredential: '자격 증명 선택…',
  secretToggleOn: 'vault secret(클릭하면 평문으로 전환)',
  secretToggleOff: '평문(클릭하면 secret으로 전환)',

  // ─── custom-node editor ────────────────────────────────────────
  editNode: '사용자 정의 노드 편집',
  fieldSummaryOptional: '요약(선택)',
  fieldUnderlyingType: '기본 유형',
  underlyingTypeHint: '기본 작업 유형은 변경할 수 없습니다',
  fieldParams: '매개변수',
  addParam: '+ 매개변수 추가',
  nodeNamePlaceholder: '예: build-and-push',
  nodeDescPlaceholder: '이 노드의 용도',
  nodeSummaryPlaceholder: '카드에 표시되는 한 줄 요약',
  noParamsHint: '아직 매개변수가 없습니다. "+ 매개변수 추가"를 클릭하여 추가하세요.',

  // ─── confirms / toasts / errors ────────────────────────────────
  confirmDeleteTemplate: '템플릿 "{name}"을(를) 삭제하시겠습니까? 이 작업은 되돌릴 수 없습니다.',
  deletedTemplate: '템플릿 "{name}"을(를) 삭제했습니다',
  confirmDeleteGroup: '변수 그룹 "{name}"을(를) 삭제하시겠습니까? 이 작업은 되돌릴 수 없습니다.',
  deletedGroup: '변수 그룹 "{name}"을(를) 삭제했습니다',
  createdGroup: '변수 그룹 "{name}"을(를) 생성했습니다',
  updatedGroup: '변수 그룹 "{name}"을(를) 업데이트했습니다',
  confirmDeleteNode: '사용자 정의 노드 "{name}"을(를) 삭제하시겠습니까? 이 작업은 되돌릴 수 없습니다.',
  deletedNode: '사용자 정의 노드 "{name}"을(를) 삭제했습니다',
  updatedNode: '사용자 정의 노드 "{name}"을(를) 업데이트했습니다',
  groupNameRequired: '변수 그룹 이름은 비워 둘 수 없습니다',
  nodeNameRequired: '사용자 정의 노드 이름은 비워 둘 수 없습니다',
  deleteFailed: '삭제 실패',
  saveFailed: '저장 실패',
  saveFailedStatus: '저장 실패 ({status})',
  loadTemplatesFailed: '템플릿 로드 실패',
  loadTemplatesFailedStatus: '템플릿 로드 실패 ({status})',
  loadGroupsFailed: '변수 그룹 로드 실패',
  loadGroupsFailedStatus: '변수 그룹 로드 실패 ({status})',
  loadNodesFailed: '사용자 정의 노드 로드 실패',
  loadNodesFailedStatus: '사용자 정의 노드 로드 실패 ({status})',
}
