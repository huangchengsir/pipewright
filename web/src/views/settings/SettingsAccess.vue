<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";
import { onBeforeRouteLeave } from "vue-router";
import { useI18n } from "vue-i18n";
import AppButton from "../../components/ui/AppButton.vue";
import PermissionGate from "../../components/PermissionGate.vue";
import { usePermissionStore } from "../../stores/permissions";
import AppSelect from "../../components/ui/AppSelect.vue";
import { useSessionStore } from "../../stores/session";
import { useToast } from "../../composables/useToast";
import { useConfirm } from "../../composables/useConfirm";
import { HttpError } from "../../api/http";
import {
  access,
  type Action,
  type AccessScope,
  type Binding,
  type Choices,
  type Choice,
  type InitialCredential,
  type Member,
  type Role,
  type RoleInput,
  type Team,
} from "../../api/access";
const { t, te, locale } = useI18n();
const session = useSessionStore(),
  toast = useToast(),
  confirm = useConfirm();
const teams = ref<Team[]>([]),
  teamId = ref(""),
  roles = ref<Role[]>([]),
  members = ref<Member[]>([]);
const permissionStore = usePermissionStore();
watch(teamId, (value) => permissionStore.setTeam(value), {flush:"sync"});
const catalog = ref<Action[]>([]),
  choices = ref<Choices>({ projects: [], environments: [] });
const capabilities = ref<Record<string, boolean>>({}),
  loading = ref(false),
  busy = ref(false),
  error = ref("");
const tab = ref<"members" | "roles" | "employees">("members"),
  selectedRole = ref(""),
  selectedPage = ref("");
const roleDraft = reactive<RoleInput>({
  name: "",
  pages: [],
  grants: [],
  requiresSelectedBinding: true,
  expectedRevision: 0,
});
const roleBaseline = ref("");
const employeeRows = ref<Member[]>([]),
  employeeCursor = ref(""),
  employeeNext = ref(""),
  employeeHistory = ref<string[]>([]);
const shownMembers = computed(() =>
  tab.value === "employees" ? employeeRows.value : members.value,
);
async function loadEmployees(cursor = ""): Promise<void> {
  const generation=identityGeneration;
  if (!currentIdentity(generation) || !isAdmin.value) return;
  loading.value = true;
  error.value = "";
  try {
    const page = await access.users(cursor);
    if (!currentIdentity(generation) || !isAdmin.value) return;
    employeeCursor.value = cursor;
    employeeNext.value = page.nextCursor;
    employeeRows.value = page.users.map((user) => ({
      id: user.id,
      teamId: "",
      userId: user.id,
      username: user.username,
      displayName: user.displayName,
      enabled: user.enabled,
      accountEnabled: user.enabled,
      revision: 0,
      bindings: [],
    }));
  } catch (err) {
    if (!currentIdentity(generation)) return;
    error.value = problem(err);
  } finally {
    loading.value = false;
  }
}
async function changeTab(
  value: "members" | "roles" | "employees",
): Promise<void> {
  tab.value = value;
  if (value === "employees") await loadEmployees(employeeCursor.value);
}
async function nextEmployees(): Promise<void> {
  employeeHistory.value.push(employeeCursor.value);
  await loadEmployees(employeeNext.value);
}
async function previousEmployees(): Promise<void> {
  await loadEmployees(employeeHistory.value.pop() ?? "");
}
async function refresh(): Promise<void> {
  if (!(await allowDiscard())) return;
  if (tab.value === "employees") await loadEmployees(employeeCursor.value);
  else await loadTeam();
}

const roleDirty = computed(
  () =>
    roleBaseline.value !== "" &&
    JSON.stringify(roleDraft) !== roleBaseline.value,
);
const isAdmin = computed(() => session.user?.instanceAdmin === true);
const canManageRoles = computed(
  () => capabilities.value["role.manage"] === true,
);
const canEditRole = computed(() =>
  selectedRole.value
    ? roles.value.find((role) => role.id === selectedRole.value)?.canManage ===
      true
    : canManageRoles.value,
);
const canManageMembers = computed(
  () => capabilities.value["member.manage"] === true,
);
const teamOptions = computed(() =>
  teams.value.map((team) => ({ value: team.id, label: team.name })),
);
const roleOptions = computed(() =>
  roles.value.map((role) => ({ value: role.id, label: roleName(role) })),
);
const pages = computed(() => [
  ...new Set(catalog.value.map((action) => action.page)),
]);
const visibleActions = computed(() =>
  catalog.value.filter((action) => action.page === selectedPage.value),
);
const scopes = computed(() =>
  ["none", "team", "selected", "own"].map((value) => ({
    value,
    label: t(`access.scope.${value}`),
  })),
);
const envPolicies = computed(() =>
  ["test", "selected"].map((value) => ({
    value,
    label: t(`access.envPolicy.${value}`),
  })),
);

const previewMember = ref(""),
  previewAction = ref(""),
  previewResource = ref("");
const previewResult = ref<{
  currentAllowed: boolean;
  draftAllowed?: boolean;
  revision: number;
} | null>(null);
const previewBusy = ref(false),
  previewError = ref("");
const previewActions = computed(() =>
  catalog.value
    .filter((action) => action.id !== "project.create")
    .map((action) => ({ value: action.id, label: actionName(action.id) })),
);
const previewMembers = computed(() =>
  members.value
    .filter((member) => member.enabled && member.accountEnabled)
    .map((member) => ({ value: member.id, label: member.displayName })),
);
const previewResources = computed(() => {
  const action = catalog.value.find(
    (action) => action.id === previewAction.value,
  );
  const resources =
    action?.resourceKind === "project"
      ? choices.value.projects
      : action?.resourceKind === "environment"
        ? choices.value.environments
        : (choices.value.resources ?? []).filter(
            (resource) => resource.kind === action?.resourceKind,
          );
  return resources.map((resource) => ({
    value: resource.id,
    label: resource.name,
  }));
});
let previewSequence = 0;
watch(
  [
    teamId,
    previewMember,
    previewAction,
    previewResource,
    () => JSON.stringify(roleDraft),
  ],
  () => {
    previewSequence++;
    previewResult.value = null;
    previewError.value = "";
  },
);
watch(previewAction, () => {
  previewResource.value = "";
});
async function runPreview(): Promise<void> {
  const generation=identityGeneration;
  if (!currentIdentity(generation)) return;
  const seq = ++previewSequence;
  previewBusy.value = true;
  previewResult.value = null;
  previewError.value = "";
  try {
    const result = await access.preview(teamId.value, {
      memberId: previewMember.value,
      action: previewAction.value,
      resourceId: previewResource.value,
      ...(selectedRole.value && roleDirty.value
        ? { draftRoleId: selectedRole.value, draft: copy(roleDraft) }
        : {}),
    });
    if (seq === previewSequence && currentIdentity(generation)) previewResult.value = result;
  } catch (err) {
    if (!currentIdentity(generation)) return;
    if (seq === previewSequence) previewError.value = problem(err);
  } finally {
    previewBusy.value = false;
  }
}
const formDialog = ref<HTMLDialogElement | null>(null),
  credentialDialog = ref<HTMLDialogElement | null>(null);
const credential = ref<InitialCredential | null>(null);
const formKind = ref<"member" | "employee" | "team" | "profile">("member");
const form = reactive({
  id: "",
  userId: "",
  username: "",
  displayName: "",
  name: "",
  enabled: true,
  revision: 0,
  bindings: [] as Binding[],
});
const formError = ref("");
const formTitle = computed(() =>
  t(
    `access.${formKind.value === "member" ? (form.id ? "editMember" : "addExisting") : formKind.value === "employee" ? "createEmployee" : formKind.value === "profile" ? "editProfile" : "createTeam"}`,
  ),
);
let loadSequence = 0;
let identityGeneration = 0;
const identityKey = computed(() => JSON.stringify([session.user?.userId ?? session.user?.username,session.user?.multiTenant,session.user?.instanceAdmin,session.user?.mustChangePassword,session.isNetworkError]));
function currentIdentity(generation: number): boolean { return generation === identityGeneration && !!session.user && !session.user.mustChangePassword && !session.isNetworkError }
function canWriteForm(): boolean {
 if (!currentIdentity(identityGeneration)) return false;
 return formKind.value !== 'member' ? isAdmin.value : permissionStore.allowed(teamId.value,{action:'member.manage',resourceId:form.id,create:!form.id});
}
watch(identityKey, () => {
 identityGeneration++; loadSequence++; previewSequence++;
 teams.value=[]; roles.value=[]; members.value=[]; employeeRows.value=[]; capabilities.value={}; choices.value={projects:[],environments:[]};
 credential.value=null; previewResult.value=null; error.value=''; formDialog.value?.close(); credentialDialog.value?.close(); discardForm(); draftFrom();
}, {flush:'sync'});
const copy = <T,>(value: T): T => JSON.parse(JSON.stringify(value)) as T;
function roleName(role: Role): string {
  return te(role.nameKey) ? t(role.nameKey) : role.nameKey;
}
function bindingName(binding: Binding): string {
  const role = roles.value.find((r) => r.id === binding.roleId);
  return role ? roleName(role) : t("access.unavailableRole");
}
function pageName(page: string): string {
  return page ? t(`access.pages.${page.replace("page.", "")}`) : "";
}
function actionName(action: string): string {
  return t(`access.actions.${action.replaceAll(".", "_")}`);
}
function problem(err: unknown): string {
  const code = err instanceof HttpError ? err.apiError?.code : "";
  return code && te(`access.errors.${code}`)
    ? t(`access.errors.${code}`)
    : t("access.errors.request_failed");
}
function draftFrom(role?: Role): void {
  selectedRole.value = role?.id ?? "";
  Object.assign(roleDraft, {
    name: role ? roleName(role) : "",
    pages: [...(role?.pages ?? [])],
    grants: copy(role?.grants ?? []),
    requiresSelectedBinding: role?.requiresSelectedBinding ?? true,
    expectedRevision: role?.revision ?? 0,
  });
  roleBaseline.value = JSON.stringify(roleDraft);
  selectedPage.value = roleDraft.pages[0] ?? pages.value[0] ?? "";
}
async function allowDiscard(): Promise<boolean> {
  return (
    !roleDirty.value ||
    confirm.open({
      title: t("access.unsaved"),
      body: t("access.discardBody"),
      confirmLabel: t("access.discard"),
    })
  );
}
async function selectRole(role?: Role): Promise<void> {
  if (await allowDiscard()) draftFrom(role);
}
async function loadTeam(): Promise<void> {
  permissionStore.invalidate();
  const generation=identityGeneration;
  const seq = ++loadSequence,
    team = teamId.value;
  roles.value = [];
  members.value = [];
  capabilities.value = {};
  choices.value = { projects: [], environments: [] };
  draftFrom();
  if (!team) return;
  loading.value = true;
  error.value = "";
  try {
    const perms = await access.capabilities(team);
    if (seq !== loadSequence || !currentIdentity(generation)) return;
    const [rs, ms, cs] = await Promise.all([
      perms["role.read"] ? access.roles(team) : Promise.resolve([]),
      perms["member.read"] ? access.members(team) : Promise.resolve([]),
      perms["choices.read"]
        ? access.choices(team)
        : Promise.resolve({ projects: [], environments: [] }),
    ]);
    if (seq !== loadSequence || !currentIdentity(generation)) return;
    roles.value = rs;
    members.value = ms;
    choices.value = cs;
    capabilities.value = perms;
    if (tab.value === "members" && !perms["member.read"])
      tab.value = perms["role.read"] ? "roles" : "members";
    if (tab.value === "roles" && !perms["role.read"]) tab.value = "members";
    draftFrom(rs[0]);
  } catch (err) {
    if (!currentIdentity(generation)) return;
    if (seq === loadSequence) error.value = problem(err);
  } finally {
    if (seq === loadSequence) loading.value = false;
  }
}
async function switchTeam(id: string): Promise<void> {
  const generation = identityGeneration;
  const sequence = loadSequence;
  if (busy.value || loading.value || id === teamId.value || !teams.value.some((team) => team.id === id)) return;
  if (!(await allowDiscard()) || !currentIdentity(generation) || sequence !== loadSequence || busy.value) return;
  teamId.value = id;
  await loadTeam();
}
async function initialize(): Promise<void> {
  const generation=identityGeneration;
  loading.value = true;
  error.value = "";
  try {
    const [ts, actions] = await Promise.all([access.teams(), access.catalog()]);
    if (!currentIdentity(generation)) return;
    teams.value = ts;
    catalog.value = actions.actions;
    teamId.value = ts.some(team=>team.id===permissionStore.activeTeam) ? permissionStore.activeTeam : ts[0]?.id ?? "";
    await loadTeam();
  } catch (err) {
    if (!currentIdentity(generation)) return;
    error.value = problem(err);
  } finally {
    loading.value = false;
  }
}
function grant(action: string) {
  return roleDraft.grants.find((item) => item.action === action);
}
function enableAction(id: string): void {
  const action = catalog.value.find((item) => item.id === id);
  if (!action || grant(id)) return;
  if (!roleDraft.pages.includes(action.page)) roleDraft.pages.push(action.page);
  roleDraft.grants.push({
    action: id,
    scope: {
      kind: "team",
      ...(action.environmentRequired
        ? { environmentPolicy: "test" as const }
        : {}),
    },
  });
  if (action.readAction) enableAction(action.readAction);
}
function disableAction(id: string): void {
  roleDraft.grants = roleDraft.grants.filter((item) => item.action !== id);
  for (const action of catalog.value)
    if (action.readAction === id) disableAction(action.id);
}
function toggleAction(action: Action, checked: boolean): void {
  if (checked) enableAction(action.id);
  else disableAction(action.id);
}
function togglePage(page: string, checked: boolean): void {
  if (checked) {
    if (!roleDraft.pages.includes(page)) roleDraft.pages.push(page);
  } else {
    roleDraft.pages = roleDraft.pages.filter((item) => item !== page);
    for (const action of catalog.value.filter((item) => item.page === page))
      disableAction(action.id);
  }
}
function changeScope(action: string, kind: string): void {
  const scope = grant(action)?.scope;
  if (scope) scope.kind = kind as AccessScope["kind"];
}
function changePolicy(action: string, value: string): void {
  const scope = grant(action)?.scope;
  if (scope) {
    scope.environmentPolicy = value as "test" | "selected";
    scope.environmentIds = [];
  }
}
function setIds(
  scope: AccessScope,
  key: "projectIds" | "environmentIds",
  id: string,
  checked: boolean,
): void {
  const values = scope[key] ?? [];
  scope[key] = checked
    ? [...new Set([...values, id])]
    : values.filter((value) => value !== id);
}
// Only project actions can treat an exact resource ID as a whole project.
// Preserve legacy representations until the user changes that checkbox.
function hasProject(scope: AccessScope, action: Action, id: string): boolean {
  return scope.projectIds?.includes(id) === true ||
    (action.resourceKind === "project" && hasResource(scope, {kind: "project", id, name: ""}));
}
function setProject(scope: AccessScope, action: Action, id: string, checked: boolean): void {
  if (action.resourceKind === "project") {
    scope.resourceIds = (scope.resourceIds ?? []).filter(value => value !== id);
    scope.resourceRefs = (scope.resourceRefs ?? []).filter(ref => ref.kind !== "project" || ref.id !== id);
  }
  setIds(scope, "projectIds", id, checked);
}
function resourcesFor(action: Action): Choice[] {
  return (choices.value.resources ?? []).filter(
    (resource) => resource.kind === action.resourceKind,
  );
}
function hasResource(scope: AccessScope, resource: Choice): boolean {
  return (
    scope.resourceRefs?.some(
      (ref) => ref.kind === resource.kind && ref.id === resource.id,
    ) === true || scope.resourceIds?.includes(resource.id) === true
  );
}
function setResource(
  scope: AccessScope,
  resource: Choice,
  checked: boolean,
): void {
  if (!resource.kind) return;
  scope.resourceIds = (scope.resourceIds ?? []).filter(
    (id) => id !== resource.id,
  );
  scope.resourceRefs = (scope.resourceRefs ?? []).filter(
    (ref) => ref.kind !== resource.kind || ref.id !== resource.id,
  );
  if (checked)
    scope.resourceRefs.push({ kind: resource.kind, id: resource.id });
}
function bindingResources(binding: Binding): Choice[] {
  const role = roles.value.find((role) => role.id === binding.roleId);
  const kinds = new Set(
    catalog.value
      .filter(
        (action) =>
          !action.projectScoped &&
          role?.grants.some(
            (grant) =>
              grant.action === action.id && grant.scope.kind !== "none",
          ),
      )
      .map((action) => action.resourceKind),
  );
  return (choices.value.resources ?? []).filter((resource) =>
    kinds.has(resource.kind ?? ""),
  );
}
function bindingEnvironments(binding: Binding): Choice[] {
  const role = roles.value.find((role) => role.id === binding.roleId);
  return choices.value.environments.filter((env) => {
    if (
      binding.scope.kind === "selected" &&
      !binding.scope.projectIds?.includes(env.projectId ?? "")
    )
      return false;
    return role?.grants.some((grant) => {
      const action = catalog.value.find((action) => action.id === grant.action);
      if (!action?.environmentRequired || grant.scope.kind === "none")
        return false;
      if (
        grant.scope.kind === "selected" &&
        !grant.scope.projectIds?.includes(env.projectId ?? "")
      )
        return false;
      if (grant.scope.environmentPolicy === "test")
        return (
          env.purpose === "test" &&
          (!grant.scope.environmentIds?.length ||
            grant.scope.environmentIds.includes(env.id))
        );
      return grant.scope.environmentIds?.includes(env.id) === true;
    });
  });
}
function roleEnvironments(action: Action): Choice[] {
  return choices.value.environments.filter(
    (env) =>
      grant(action.id)?.scope.environmentPolicy !== "test" ||
      env.purpose === "test",
  );
}
async function saveRole(): Promise<void> {
  const generation=identityGeneration;
  const team=teamId.value;
  if (busy.value || !currentIdentity(generation) || !permissionStore.allowed(teamId.value,{action:'role.manage',resourceId:selectedRole.value,create:!selectedRole.value})) return;
  busy.value = true;
  try {
    const current = roles.value.find((role) => role.id === selectedRole.value);
    const input = copy(roleDraft);
    if (current && input.name === roleName(current))
      input.name = current.nameKey;
    const saved = await access.saveRole(
      teamId.value,
      selectedRole.value,
      input,
    );
    if (!currentIdentity(generation) || team !== teamId.value) return;
    roles.value = [
      ...roles.value.filter((role) => role.id !== saved.id),
      saved,
    ];
    draftFrom(saved);
    toast.success(t("access.saved"));
  } catch (err) {
    if (!currentIdentity(generation)) return;
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function deleteRole(): Promise<void> {
  const generation=identityGeneration;
  const team=teamId.value;
  const role = roles.value.find((item) => item.id === selectedRole.value);
  if (!role || busy.value || !currentIdentity(generation) || !permissionStore.allowed(team,{action:'role.manage',resourceId:role.id})) return;
  if (
    !(await confirm.open({
      title: t("access.deleteRole"),
      body: t("access.deleteRoleBody", { name: roleName(role) }),
      variant: "danger",
    }))
  )
    return;
  busy.value = true;
  try {
    if (!currentIdentity(generation) || team !== teamId.value || !permissionStore.allowed(team,{action:'role.manage',resourceId:role.id})) return;
    await access.deleteRole(team, role);
    if (!currentIdentity(generation)) return;
    roleBaseline.value = "";
    await loadTeam();
    toast.success(t("access.saved"));
  } catch (err) {
    if (!currentIdentity(generation)) return;
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
function addBinding(): void {
  const role =
    roles.value.find((item) => item.presetKey === "viewer") ?? roles.value[0];
  if (role)
    form.bindings.push({
      roleId: role.id,
      scope: {
        kind: role.requiresSelectedBinding ? "selected" : "team",
        projectIds: [],
        environmentIds: [],
      },
    });
}
function bindingRoleChanged(binding: Binding): void {
  const role = roles.value.find((item) => item.id === binding.roleId);
  if (role?.requiresSelectedBinding && binding.scope.kind !== "none")
    binding.scope.kind = "selected";
}
function bindingScopeOptions(binding: Binding) {
  return scopes.value.filter(
    (option) =>
      option.value === "none" ||
      option.value === "selected" ||
      (option.value === "team" &&
        !roles.value.find((role) => role.id === binding.roleId)
          ?.requiresSelectedBinding),
  );
}
async function openForm(
  kind: typeof formKind.value,
  member?: Member,
): Promise<void> {
  if (!currentIdentity(identityGeneration) || (kind !== 'member' ? !isAdmin.value : !permissionStore.allowed(teamId.value,{action:'member.manage',resourceId:member?.id ?? '',create:!member}))) return;
  formKind.value = kind;
  formError.value = "";
  Object.assign(form, {
    id: member?.id ?? "",
    userId: member?.userId ?? "",
    username: member?.username ?? "",
    displayName: member?.displayName ?? "",
    name: "",
    enabled: member?.enabled ?? true,
    revision: member?.revision ?? 0,
    bindings: copy(member?.bindings ?? []),
  });
  if ((kind === "member" || kind === "employee") && !member) addBinding();
  await nextTick();
  formDialog.value?.showModal();
}
function closeForm(): void {
  if (!busy.value) formDialog.value?.close();
}
function discardForm(): void {
  formError.value = "";
  form.username = "";
  form.displayName = "";
  form.bindings = [];
}
async function showCredential(value: InitialCredential): Promise<void> {
  credential.value = value;
  await nextTick();
  credentialDialog.value?.showModal();
}
async function submitForm(): Promise<void> {
  const generation=identityGeneration;
  if (busy.value || !canWriteForm()) return;
  busy.value = true;
  formError.value = "";
  const team = teamId.value;
  try {
    let initial: InitialCredential | undefined;
    if (formKind.value === "team") {
      const created = await access.createTeam(form.name.trim());
      if (!currentIdentity(generation)) return;
      const refreshed=await access.teams();
      if (!currentIdentity(generation)) return;
      teams.value = refreshed;
      teamId.value = created.id;
    } else if (formKind.value === "profile")
      await access.updateUser(form.userId, form.displayName.trim());
    else if (formKind.value === "employee")
      initial = await access.createUser(
        form.username.trim(),
        form.displayName.trim(),
        team,
        copy(form.bindings),
      );
    else {
      const userId = form.id
        ? form.userId
        : (await access.lookup(team, form.username.trim())).id;
      if (!currentIdentity(generation) || team !== teamId.value || !canWriteForm()) return;
      await access.saveMember(team, form.id, {
        userId,
        enabled: form.enabled,
        bindings: copy(form.bindings),
        expectedRevision: form.revision,
      });
    }
    if (!currentIdentity(generation)) return;
    formDialog.value?.close();
    if (tab.value === "employees") await loadEmployees(employeeCursor.value);
    else await loadTeam();
    toast.success(t("access.saved"));
    if (initial && currentIdentity(generation) && isAdmin.value) await showCredential(initial);
  } catch (err) {
    if (!currentIdentity(generation)) return;
    formError.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function resetPassword(member: Member): Promise<void> {
  const generation=identityGeneration;
  if (!currentIdentity(generation) || !isAdmin.value || member.userId === session.user?.userId) return;
  if (
    busy.value ||
    !(await confirm.open({
      title: t("access.resetPassword"),
      body: t("access.resetBody", { name: member.displayName }),
      variant: "danger",
    }))
  )
    return;
  busy.value = true;
  try {
    if (!currentIdentity(generation) || !isAdmin.value) return;
    const result=await access.resetPassword(member.userId);
    if (currentIdentity(generation) && isAdmin.value) await showCredential(result);
  } catch (err) {
    if (!currentIdentity(generation)) return;
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function toggleAccount(member: Member): Promise<void> {
  const generation=identityGeneration;
  if (!currentIdentity(generation) || !isAdmin.value || member.userId === session.user?.userId) return;
  if (
    busy.value ||
    !(await confirm.open({
      title: t(member.accountEnabled ? "access.disable" : "access.enable"),
      body: t("access.accountStateBody", { name: member.displayName }),
      variant: "danger",
    }))
  )
    return;
  busy.value = true;
  try {
    if (!currentIdentity(generation) || !isAdmin.value) return;
    await access.setUserEnabled(member.userId, !member.accountEnabled);
    if (!currentIdentity(generation)) return;
    if (tab.value === "employees") await loadEmployees(employeeCursor.value);
    else await loadTeam();
  } catch (err) {
    if (!currentIdentity(generation)) return;
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function copyCredential(): Promise<void> {
  if (!credential.value) return;
  try {
    await navigator.clipboard.writeText(credential.value.temporaryPassword);
    toast.success(t("access.copied"));
  } catch {
    toast.error(t("access.copyFailed"));
  }
}
onBeforeRouteLeave(allowDiscard);
onBeforeUnmount(() => {
  identityGeneration++;
  loadSequence++;
  credential.value = null;
  formDialog.value?.close();
  credentialDialog.value?.close();
});
onMounted(initialize);
</script>

<template>
  <section class="access-page settings-page">
    <header class="access-heading">
      <div>
        <h2>{{ t("access.title") }}</h2>
        <p>{{ t("access.subtitle") }}</p>
      </div>
      <AppButton
        v-if="isAdmin"
        :disabled="busy || loading"
        @click="openForm('team')"
        >{{ t("access.createTeam") }}</AppButton
      >
    </header>
    <div class="access-toolbar">
      <AppSelect
                    portal
        :model-value="teamId"
        :options="teamOptions"
        :aria-label="t('access.team')"
        :disabled="busy || loading"
        @update:model-value="switchTeam"
      />
      <div class="access-tabs" :aria-label="t('access.section')">
        <button
          v-if="capabilities['member.read']"
          :aria-pressed="tab === 'members'"
          @click="changeTab('members')"
        >
          {{ t("access.members") }} <span>{{ members.length }}</span></button
        ><button
          v-if="capabilities['role.read']"
          :aria-pressed="tab === 'roles'"
          @click="changeTab('roles')"
        >
          {{ t("access.rolesTitle") }} <span>{{ roles.length }}</span></button
        ><button
          v-if="isAdmin"
          :aria-pressed="tab === 'employees'"
          @click="changeTab('employees')"
        >
          {{ t("access.employees") }}
        </button>
      </div>
    </div>
    <p v-if="error" class="access-error" role="alert">
      {{ error }}
      <AppButton variant="ghost" :disabled="loading || busy" @click="refresh">{{
        t("access.refresh")
      }}</AppButton>
    </p>
    <p v-if="loading" class="access-empty" role="status">
      {{ t("access.loading") }}
    </p>
    <p v-else-if="!teamId" class="access-empty">{{ t("access.noTeams") }}</p>
    <template v-else>
      <section v-if="tab !== 'roles'" class="access-panel">
        <header class="panel-heading">
          <div>
            <h3>
              {{
                t(tab === "employees" ? "access.employees" : "access.members")
              }}
            </h3>
            <p>
              {{
                t(
                  tab === "employees"
                    ? "access.employeesHint"
                    : "access.memberHint",
                )
              }}
            </p>
          </div>
          <div class="actions">
            <PermissionGate :team="teamId" :query="{action: 'member.manage', resourceId: '', create: true}"><AppButton
              v-if="canManageMembers && tab !== 'employees'"
              :disabled="busy"
              @click="openForm('member')"
              >{{ t("access.addExisting") }}</AppButton
            ></PermissionGate><AppButton
              v-if="isAdmin"
              variant="primary"
              :disabled="busy"
              @click="openForm('employee')"
              >{{ t("access.createEmployee") }}</AppButton
            >
          </div>
        </header>
        <div class="access-table-wrap">
          <table>
            <thead>
              <tr>
                <th>{{ t("access.person") }}</th>
                <th>{{ t("access.rolesTitle") }}</th>
                <th>{{ t("access.scopeTitle") }}</th>
                <th>{{ t("access.status") }}</th>
                <th>
                  <span class="sr-only">{{ t("access.operations") }}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="member in shownMembers" :key="member.id">
                <td>
                  <strong>{{ member.displayName }}</strong
                  ><small>{{ member.username }}</small>
                </td>
                <td>
                  <span
                    v-for="binding in member.bindings"
                    :key="binding.roleId"
                    class="access-chip"
                    >{{ bindingName(binding) }}</span
                  ><span v-if="!member.bindings.length">{{
                    t("access.noRoles")
                  }}</span>
                </td>
                <td>
                  <small
                    v-for="binding in member.bindings"
                    :key="binding.roleId"
                    >{{
                      binding.scope.kind === "selected"
                        ? t("access.projectCount", {
                            count: binding.scope.projectIds?.length ?? 0,
                          })
                        : t(`access.scope.${binding.scope.kind}`)
                    }}</small
                  >
                </td>
                <td>
                  <span
                    class="state-dot"
                    :class="{
                      inactive: !member.enabled || !member.accountEnabled,
                    }"
                  />{{
                    t(
                      member.enabled && member.accountEnabled
                        ? "access.active"
                        : "access.disabled",
                    )
                  }}
                </td>
                <td>
                  <div class="row-actions">
                    <PermissionGate :team="teamId" :query="{action: 'member.manage', resourceId: member.id}"><AppButton
                      v-if="member.canManage && tab !== 'employees'"
                      variant="ghost"
                      :disabled="busy"
                      @click="openForm('member', member)"
                      >{{ t("access.permissions") }}</AppButton
                    ></PermissionGate>
                    <details v-if="isAdmin" class="member-menu">
                      <summary>{{ t("access.more") }}</summary>
                      <div>
                        <button
                          :disabled="busy"
                          @click="openForm('profile', member)"
                        >
                          {{ t("access.editProfile") }}</button
                        ><button
                          :disabled="
                            busy || member.userId === session.user?.userId
                          "
                          @click="resetPassword(member)"
                        >
                          {{ t("access.resetPassword") }}</button
                        ><button
                          :disabled="
                            busy || member.userId === session.user?.userId
                          "
                          @click="toggleAccount(member)"
                        >
                          {{
                            t(
                              member.accountEnabled
                                ? "access.disable"
                                : "access.enable",
                            )
                          }}
                        </button>
                      </div>
                    </details>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="!shownMembers.length" class="access-empty">
          {{ t("access.noMembers") }}
        </p>
        <footer v-if="tab === 'employees'" class="panel-footer">
          <span />
          <div class="actions">
            <AppButton
              :disabled="loading || !employeeHistory.length"
              @click="previousEmployees"
              >{{ t("access.previous") }}</AppButton
            ><AppButton
              :disabled="loading || !employeeNext"
              @click="nextEmployees"
              >{{ t("access.next") }}</AppButton
            >
          </div>
        </footer>
      </section>
      <div v-else class="role-layout">
        <aside class="role-list">
          <div class="panel-heading">
            <h3>{{ t("access.rolesTitle") }}</h3>
            <AppButton
              v-if="canManageRoles"
              variant="ghost"
              :disabled="busy"
              @click="selectRole()"
              >{{ t("access.add") }}</AppButton
            >
          </div>
          <button
            v-for="role in roles"
            :key="role.id"
            class="role-item"
            :aria-pressed="selectedRole === role.id"
            :disabled="busy"
            @click="selectRole(role)"
          >
            <strong>{{ roleName(role) }}</strong
            ><small>{{
              role.requiresSelectedBinding
                ? t("access.assignedProjects")
                : t("access.teamRole")
            }}</small
            ><span>{{
              t("access.actionCount", { count: role.grants.length })
            }}</span>
          </button>
        </aside>
        <section class="access-panel role-editor">
          <header class="panel-heading">
            <div>
              <h3>
                {{ selectedRole ? t("access.editRole") : t("access.newRole") }}
              </h3>
              <p>{{ t("access.roleHint") }}</p>
            </div>
            <span v-if="roleDirty" class="access-chip">{{
              t("access.unsaved")
            }}</span>
          </header>
          <div class="role-basics">
            <label
              >{{ t("access.roleName")
              }}<input
                v-model="roleDraft.name"
                maxlength="80"
                :disabled="!canEditRole || busy" /></label
            ><label class="check-label"
              ><input
                v-model="roleDraft.requiresSelectedBinding"
                type="checkbox"
                :disabled="!canEditRole || busy"
              />{{ t("access.limitMembers") }}</label
            >
            <p class="quiet">{{ t("access.assignedHint") }}</p>
          </div>
          <div class="permission-grid">
            <nav class="menu-tree" :aria-label="t('access.menus')">
              <h4>{{ t("access.menus") }}</h4>
              <div
                v-for="page in pages"
                :key="page"
                class="menu-item"
                :class="{ selected: selectedPage === page }"
              >
                <input
                  type="checkbox"
                  :aria-label="pageName(page)"
                  :checked="roleDraft.pages.includes(page)"
                  :disabled="!canEditRole || busy"
                  @change="
                    togglePage(
                      page,
                      ($event.target as HTMLInputElement).checked,
                    )
                  "
                /><button @click="selectedPage = page">
                  {{ pageName(page) }}
                </button>
              </div>
            </nav>
            <div class="action-editor">
              <h4>
                {{ pageName(selectedPage) }}
                <small>{{ t("access.actionsTitle") }}</small>
              </h4>
              <p v-if="!roleDraft.pages.includes(selectedPage)" class="quiet">
                {{ t("access.menuOff") }}
              </p>
              <div
                v-for="action in visibleActions"
                :key="action.id"
                class="action-row"
              >
                <div class="action-line">
                  <label class="check-label"
                    ><input
                      type="checkbox"
                      :checked="!!grant(action.id)"
                      :disabled="
                        !canEditRole ||
                        busy ||
                        !roleDraft.pages.includes(action.page)
                      "
                      @change="
                        toggleAction(
                          action,
                          ($event.target as HTMLInputElement).checked,
                        )
                      "
                    />{{ actionName(action.id)
                    }}<span
                      v-if="action.sensitive"
                      class="sensitive-mark"
                      :title="t('access.sensitive')"
                      >·</span
                    ></label
                  ><AppSelect
                    portal
                    v-if="grant(action.id)"
                    :model-value="grant(action.id)!.scope.kind"
                    :options="scopes"
                    :aria-label="
                      t('access.scopeFor', { action: actionName(action.id) })
                    "
                    :disabled="!canEditRole || busy"
                    min-width="140px"
                    @update:model-value="changeScope(action.id, $event)"
                  />
                </div>
                <template v-if="grant(action.id)"
                  ><div
                    v-if="
                      grant(action.id)!.scope.kind === 'selected' &&
                      action.projectScoped
                    "
                    class="scope-picker"
                  >
                    <label
                      v-for="project in choices.projects"
                      :key="project.id"
                      class="check-label"
                      ><input
                        type="checkbox"
                        :checked="
                          hasProject(grant(action.id)!.scope, action, project.id)
                        "
                        :disabled="!canEditRole || busy"
                        @change="
                          setProject(
                            grant(action.id)!.scope,
                            action,
                            project.id,
                            ($event.target as HTMLInputElement).checked,
                          )
                        "
                      />{{ project.name }}</label
                    ><small v-if="!choices.projects.length">{{
                      t("access.noProjects")
                    }}</small>
                  </div>
                  <div
                    v-if="
                      grant(action.id)!.scope.kind === 'selected' &&
                      !action.projectScoped
                    "
                    class="scope-picker"
                  >
                    <label
                      v-for="resource in resourcesFor(action)"
                      :key="resource.kind + ':' + resource.id"
                      class="check-label"
                    >
                      <input
                        type="checkbox"
                        :checked="
                          hasResource(grant(action.id)!.scope, resource)
                        "
                        :disabled="!canEditRole || busy"
                        @change="
                          setResource(
                            grant(action.id)!.scope,
                            resource,
                            ($event.target as HTMLInputElement).checked,
                          )
                        "
                      />{{ resource.name }}
                    </label>
                    <small v-if="!resourcesFor(action).length">{{
                      t("access.resourceScopeHint")
                    }}</small>
                  </div>
                  <div
                    v-if="action.environmentRequired"
                    class="environment-scope"
                  >
                    <AppSelect
                    portal
                      :model-value="
                        grant(action.id)!.scope.environmentPolicy || 'selected'
                      "
                      :options="envPolicies"
                      :aria-label="t('access.environments')"
                      :disabled="!canEditRole || busy"
                      @update:model-value="changePolicy(action.id, $event)"
                    />
                    <div
                      v-if="
                        grant(action.id)!.scope.environmentPolicy !== 'test' ||
                        !!grant(action.id)!.scope.environmentIds?.length
                      "
                      class="scope-picker"
                    >
                      <label
                        v-for="env in roleEnvironments(action)"
                        :key="env.id"
                        class="check-label"
                        ><input
                          type="checkbox"
                          :checked="
                            grant(action.id)!.scope.environmentIds?.includes(
                              env.id,
                            )
                          "
                          :disabled="!canEditRole || busy"
                          @change="
                            setIds(
                              grant(action.id)!.scope,
                              'environmentIds',
                              env.id,
                              ($event.target as HTMLInputElement).checked,
                            )
                          "
                        />{{ env.name }}</label
                      >
                    </div>
                    <small>{{ t("access.environmentHint") }}</small>
                  </div></template
                >
              </div>
            </div>
          </div>
          <section
            v-if="capabilities['member.read']"
            class="role-basics"
            :aria-label="t('access.previewTitle')"
          >
            <h4>{{ t("access.previewTitle") }}</h4>
            <p class="quiet">{{ t("access.previewHint") }}</p>
            <div class="actions">
              <AppSelect
                    portal
                v-model="previewMember"
                :options="previewMembers"
                :aria-label="t('access.person')"
              />
              <AppSelect
                    portal
                v-model="previewAction"
                :options="previewActions"
                :aria-label="t('access.actionsTitle')"
              />
              <AppSelect
                    portal
                v-model="previewResource"
                :options="previewResources"
                :aria-label="t('access.scopeTitle')"
              />
              <AppButton
                :loading="previewBusy"
                :disabled="!previewMember || !previewAction || !previewResource"
                @click="runPreview"
                >{{ t("access.previewCheck") }}</AppButton
              >
            </div>
            <p v-if="previewError" role="alert">{{ previewError }}</p>
            <div v-if="previewResult" role="status">
              <p>
                {{ t("access.previewCurrent") }} ·
                {{
                  t(
                    previewResult.currentAllowed
                      ? "access.previewAllowed"
                      : "access.previewDenied",
                  )
                }}
              </p>
              <p v-if="previewResult.draftAllowed !== undefined">
                {{ t("access.previewDraft") }} ·
                {{
                  t(
                    previewResult.draftAllowed
                      ? "access.previewAllowed"
                      : "access.previewDenied",
                  )
                }}
              </p>
            </div>
          </section>
          <footer class="panel-footer">
            <span class="quiet">{{ t("access.defaultDeny") }}</span>
            <div class="actions">
              <PermissionGate :team="teamId" :query="{action: 'role.manage', resourceId: selectedRole}"><AppButton
                v-if="canEditRole && selectedRole"
                variant="ghost"
                :disabled="busy"
                @click="deleteRole"
                >{{ t("access.deleteRole") }}</AppButton
              ></PermissionGate><PermissionGate :team="teamId" :query="{action: 'role.manage', resourceId: selectedRole || '', create: !selectedRole}"><AppButton
                v-if="canEditRole"
                variant="primary"
                :disabled="!roleDraft.name.trim() || !roleDirty"
                :loading="busy"
                @click="saveRole"
                >{{ t("access.save") }}</AppButton
              ></PermissionGate>
            </div>
          </footer>
        </section>
      </div>
    </template>

    <dialog
      ref="formDialog"
      class="access-dialog settings-dialog"
      aria-labelledby="access-form-title"
      @cancel="busy && $event.preventDefault()"
      @close="discardForm"
    >
      <form @submit.prevent="submitForm">
        <header class="panel-heading">
          <h3 id="access-form-title">{{ formTitle }}</h3>
          <AppButton variant="ghost" :disabled="busy" @click="closeForm">{{
            t("access.close")
          }}</AppButton>
        </header>
        <div class="dialog-body">
          <p v-if="formError" role="alert" class="access-error">
            {{ formError }}
          </p>
          <label v-if="formKind === 'team'"
            >{{ t("access.teamName")
            }}<input
              v-model="form.name"
              required
              maxlength="80"
              :disabled="busy" /></label
          ><template v-else
            ><label v-if="formKind !== 'profile'"
              >{{ t("access.username")
              }}<input
                v-model="form.username"
                required
                maxlength="64"
                autocomplete="off"
                :readonly="!!form.id"
                :disabled="busy" /></label
            ><label v-if="formKind === 'employee' || formKind === 'profile'"
              >{{ t("access.displayName")
              }}<input
                v-model="form.displayName"
                required
                maxlength="80"
                :disabled="busy"
            /></label>
            <p v-if="formKind === 'employee'" class="quiet">
              {{ t("access.employeeHint") }}
            </p>
            <p v-if="formKind === 'member' && !form.id" class="quiet">
              {{ t("access.existingHint") }}
            </p>
            <template v-if="formKind !== 'profile'"
              ><label v-if="form.id" class="check-label"
                ><input
                  v-model="form.enabled"
                  type="checkbox"
                  :disabled="busy"
                />{{ t("access.memberEnabled") }}</label
              >
              <section
                v-for="(binding, index) in form.bindings"
                :key="index"
                class="binding-card"
              >
                <header>
                  <AppSelect
                    portal
                    v-model="binding.roleId"
                    :options="roleOptions"
                    :aria-label="t('access.rolesTitle')"
                    :disabled="busy"
                    @change="bindingRoleChanged(binding)"
                  /><AppButton
                    variant="ghost"
                    :disabled="busy"
                    @click="form.bindings.splice(index, 1)"
                    >{{ t("access.remove") }}</AppButton
                  >
                </header>
                <AppSelect
                    portal
                  v-model="binding.scope.kind"
                  :options="bindingScopeOptions(binding)"
                  :aria-label="t('access.scopeTitle')"
                  :disabled="busy"
                />
                <div
                  v-if="binding.scope.kind === 'selected'"
                  class="scope-picker"
                >
                  <label
                    v-for="project in choices.projects"
                    :key="project.id"
                    class="check-label"
                    ><input
                      type="checkbox"
                      :checked="binding.scope.projectIds?.includes(project.id)"
                      :disabled="busy"
                      @change="
                        setIds(
                          binding.scope,
                          'projectIds',
                          project.id,
                          ($event.target as HTMLInputElement).checked,
                        )
                      "
                    />{{ project.name }}</label
                  ><small v-if="!binding.scope.projectIds?.length">{{
                    t("access.emptyBinding")
                  }}</small>
                </div>
                <div
                  v-if="
                    binding.scope.kind === 'selected' &&
                    bindingResources(binding).length
                  "
                  class="scope-picker"
                >
                  <label
                    v-for="resource in bindingResources(binding)"
                    :key="resource.kind + ':' + resource.id"
                    class="check-label"
                  >
                    <input
                      type="checkbox"
                      :checked="hasResource(binding.scope, resource)"
                      :disabled="busy"
                      @change="
                        setResource(
                          binding.scope,
                          resource,
                          ($event.target as HTMLInputElement).checked,
                        )
                      "
                    />{{ resource.name }}
                  </label>
                </div>
                <details>
                  <summary>
                    {{ t("access.environments") }} ·
                    {{ binding.scope.environmentIds?.length ?? 0 }}
                  </summary>
                  <div class="scope-picker">
                    <label
                      v-for="env in bindingEnvironments(binding)"
                      :key="env.id"
                      class="check-label"
                      ><input
                        type="checkbox"
                        :checked="
                          binding.scope.environmentIds?.includes(env.id)
                        "
                        :disabled="busy"
                        @change="
                          setIds(
                            binding.scope,
                            'environmentIds',
                            env.id,
                            ($event.target as HTMLInputElement).checked,
                          )
                        "
                      />{{ env.name }}</label
                    ><small v-if="!bindingEnvironments(binding).length">{{
                      t("access.noTestEnvironments")
                    }}</small
                    ><small>{{ t("access.environmentHint") }}</small>
                  </div>
                </details>
              </section>
              <AppButton
                :disabled="busy || !roles.length || form.bindings.length >= 32"
                @click="addBinding"
                >{{ t("access.addRole") }}</AppButton
              ></template
            ></template
          >
        </div>
        <footer class="panel-footer">
          <span />
          <div class="actions">
            <AppButton :disabled="busy" @click="closeForm">{{
              t("access.cancel")
            }}</AppButton
            ><AppButton type="submit" variant="primary" :loading="busy">{{
              t("access.save")
            }}</AppButton>
          </div>
        </footer>
      </form>
    </dialog>
    <dialog
      ref="credentialDialog"
      class="access-dialog credential-dialog settings-dialog"
      aria-labelledby="access-credential-title"
      @close="credential = null"
    >
      <header class="panel-heading">
        <h3 id="access-credential-title">{{ t("access.initialPassword") }}</h3>
        <AppButton variant="ghost" @click="credentialDialog?.close()">{{
          t("access.close")
        }}</AppButton>
      </header>
      <div v-if="credential" class="dialog-body">
        <strong
          >{{ credential.user.displayName }} ·
          {{ credential.user.username }}</strong
        >
        <p>{{ t("access.credentialHint") }}</p>
        <div class="credential-value">
          <code>{{ credential.temporaryPassword }}</code
          ><AppButton @click="copyCredential">{{ t("access.copy") }}</AppButton>
        </div>
        <small>{{
          t("access.expires", {
            time: new Date(credential.expiresAt * 1000).toLocaleString(locale),
          })
        }}</small>
      </div>
      <footer class="panel-footer">
        <span /><AppButton
          variant="primary"
          @click="credentialDialog?.close()"
          >{{ t("access.done") }}</AppButton
        >
      </footer>
    </dialog>
  </section>
</template>
<style scoped>
.access-page {
  display: grid;
  gap: 18px;
  color: var(--color-text);
  font-size: 13px;
}
.access-heading,
.access-toolbar,
.panel-heading,
.panel-footer,
.actions,
.row-actions,
.action-line,
.binding-card > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.access-heading h2 {
  font-size: 18px;
  margin: 0 0 6px;
  font-weight: 650;
}
.access-heading p,
.panel-heading p {
  margin: 4px 0 0;
  color: var(--color-dim);
  font-size: 12px;
  line-height: 1.6;
}
.access-toolbar {
  justify-content: flex-start;
  padding-bottom: 4px;
}
.access-tabs {
  display: flex;
  gap: 4px;
  padding: 3px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: 8px;
}
.access-tabs button {
  border: 0;
  background: transparent;
  padding: 7px 12px;
  color: var(--color-dim);
  border-radius: 5px;
  cursor: pointer;
  font: inherit;
}
.access-tabs button[aria-pressed="true"] {
  background: var(--color-card);
  color: var(--color-text);
  box-shadow: 0 1px 4px #00000012;
}
.access-tabs span {
  font-size: 11px;
  margin-left: 5px;
  color: var(--color-faint);
}
.access-panel,
.role-list {
  border: 1px solid var(--color-border);
  background: var(--color-card);
  border-radius: 10px;
  min-width: 0;
}
.panel-heading {
  padding: 16px 18px;
  border-bottom: 1px solid var(--color-border);
}
h3,
h4 {
  font-size: 14px;
  margin: 0;
  font-weight: 600;
}
h4 small {
  display: block;
  font-weight: 400;
  font-size: 11px;
  color: var(--color-dim);
  margin-top: 5px;
}
.access-table-wrap {
  overflow: auto;
  min-height: 180px;
}
table {
  border-collapse: collapse;
  width: 100%;
  text-align: left;
}
th {
  font-size: 11px;
  letter-spacing: 0.03em;
  color: var(--color-dim);
  font-weight: 500;
  background: var(--color-inset);
}
th,
td {
  padding: 12px 16px;
  border-bottom: 1px solid var(--color-border);
  vertical-align: middle;
}
td strong {
  font-size: 13px;
  font-weight: 550;
}
td small {
  display: block;
  font-size: 11px;
  color: var(--color-dim);
  line-height: 1.7;
}
.access-chip {
  display: inline-block;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  padding: 3px 7px;
  border-radius: 5px;
  font-size: 11px;
  margin: 2px 4px 2px 0;
}
.state-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  background: var(--color-success, #39a274);
  border-radius: 50%;
  margin-right: 7px;
}
.state-dot.inactive {
  background: var(--color-faint);
}
.row-actions {
  justify-content: flex-end;
  white-space: nowrap;
}
.member-menu {
  position: relative;
}
.member-menu summary {
  cursor: pointer;
  list-style: none;
  padding: 8px;
}
.member-menu > div {
  position: absolute;
  right: 0;
  top: 100%;
  z-index: 10;
  min-width: 150px;
  display: grid;
  padding: 5px;
  border: 1px solid var(--color-border-strong);
  border-radius: 8px;
  background: var(--color-card);
  box-shadow: 0 8px 30px #0002;
}
.member-menu button {
  border: 0;
  background: none;
  color: var(--color-text);
  font: inherit;
  text-align: left;
  padding: 9px;
  cursor: pointer;
  border-radius: 4px;
}
.member-menu button:hover {
  background: var(--color-card-2);
}
button:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
.role-layout {
  display: grid;
  grid-template-columns: 210px minmax(0, 1fr);
  gap: 16px;
  align-items: start;
}
.role-list {
  overflow: hidden;
}
.role-item {
  border: 0;
  border-left: 2px solid transparent;
  border-bottom: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-text);
  font: inherit;
  text-align: left;
  display: grid;
  width: 100%;
  gap: 5px;
  padding: 14px 16px;
  cursor: pointer;
}
.role-item[aria-pressed="true"] {
  border-left-color: var(--color-primary);
  background: color-mix(in srgb, var(--color-primary) 8%, transparent);
}
.role-item strong {
  font-size: 13px;
  font-weight: 600;
}
.role-item small,
.role-item span {
  font-size: 11px;
  color: var(--color-dim);
  line-height: 1.5;
}
.role-item span {
  color: var(--color-faint);
}
.role-basics {
  padding: 16px 18px;
  display: grid;
  gap: 12px;
  border-bottom: 1px solid var(--color-border);
}
label:not(.check-label) {
  display: grid;
  gap: 7px;
  font-size: 12px;
  font-weight: 500;
}
input:not([type="checkbox"]) {
  height: 36px;
  border: 1px solid var(--color-border-strong);
  border-radius: 6px;
  background: var(--color-inset);
  color: var(--color-text);
  font: inherit;
  padding: 0 10px;
  min-width: 0;
  width: 100%;
  box-sizing: border-box;
}
input[type="checkbox"] {
  accent-color: var(--color-primary);
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  margin: 0;
}
.check-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  line-height: 1.6;
}
.quiet {
  font-size: 11px;
  color: var(--color-dim);
  line-height: 1.6;
  margin: 0;
}
.permission-grid {
  display: grid;
  grid-template-columns: 160px minmax(0, 1fr);
  min-height: 350px;
}
.menu-tree {
  padding: 14px 8px;
  border-right: 1px solid var(--color-border);
}
.menu-tree h4 {
  padding: 0 10px 10px;
  font-size: 11px;
  color: var(--color-dim);
}
.menu-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 8px;
  border-radius: 5px;
}
.menu-item.selected {
  background: var(--color-inset);
}
.menu-item button {
  border: 0;
  background: none;
  color: var(--color-dim);
  font: inherit;
  text-align: left;
  padding: 9px 0;
  width: 100%;
  cursor: pointer;
  font-size: 12px;
}
.menu-item.selected button {
  color: var(--color-primary);
}
.action-editor {
  padding: 16px;
  min-width: 0;
}
.action-editor > h4 {
  padding-bottom: 14px;
}
.action-row {
  padding: 12px 0;
  border-bottom: 1px solid var(--color-border);
}
.action-line {
  flex-wrap: wrap;
}
.sensitive-mark {
  color: var(--color-warning, #bf8626);
  font-size: 22px;
  line-height: 12px;
}
.scope-picker {
  display: flex;
  flex-wrap: wrap;
  gap: 9px 14px;
  padding: 12px 0 0;
}
.scope-picker small {
  flex-basis: 100%;
  color: var(--color-dim);
  font-size: 11px;
  line-height: 1.6;
}
.environment-scope {
  display: grid;
  gap: 9px;
  padding: 12px 0 0 22px;
}
.environment-scope > small {
  font-size: 11px;
  color: var(--color-dim);
  line-height: 1.6;
}
.panel-footer {
  padding: 13px 18px;
  border-top: 1px solid var(--color-border);
  flex-wrap: wrap;
}
.access-empty {
  padding: 35px 18px;
  text-align: center;
  color: var(--color-dim);
}
.access-error {
  padding: 10px 13px;
  border-radius: 6px;
  color: var(--color-danger, #c74646);
  background: color-mix(in srgb, var(--color-danger, #c74646) 7%, transparent);
  font-size: 12px;
  line-height: 1.6;
  margin: 0;
}
.access-dialog {
  border: 1px solid var(--color-border-strong);
  padding: 0;
  border-radius: 12px;
  width: min(600px, calc(100vw - 32px));
  max-height: calc(100dvh - 40px);
  overflow: auto;
  background: var(--color-card);
  color: var(--color-text);
  box-shadow: 0 20px 80px #0004;
  box-sizing: border-box;
}
.access-dialog::backdrop {
  background: #10182870;
  backdrop-filter: blur(2px);
}
.dialog-body {
  padding: 20px;
  display: grid;
  gap: 16px;
  font-size: 13px;
}
.binding-card {
  display: grid;
  gap: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  padding: 14px;
  background: var(--color-inset);
}
.binding-card summary {
  font-size: 12px;
  color: var(--color-dim);
  cursor: pointer;
  padding: 5px 0;
}
.credential-dialog {
  width: min(520px, calc(100vw - 32px));
}
.credential-value {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border-radius: 8px;
  background: var(--color-inset);
}
.credential-value code {
  overflow-wrap: anywhere;
  font-size: 13px;
  flex: 1;
}
.dialog-body > p,
.dialog-body > small {
  color: var(--color-dim);
  line-height: 1.6;
  margin: 0;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}
@media (max-width: 900px) {
  .role-layout {
    grid-template-columns: 1fr;
  }
  .role-list {
    display: flex;
    overflow-x: auto;
  }
  .role-list > .panel-heading {
    border-bottom: 0;
  }
  .role-item {
    min-width: 170px;
    border-left: 0;
    border-bottom: 2px solid transparent;
  }
  .role-item[aria-pressed="true"] {
    border-bottom-color: var(--color-primary);
  }
  .permission-grid {
    grid-template-columns: 140px minmax(0, 1fr);
  }
}
@media (max-width: 639px) {
  .access-heading,
  .panel-heading,
  .access-toolbar {
    flex-wrap: wrap;
  }
  .access-toolbar > .access-tabs {
    width: 100%;
  }
  .access-tabs button {
    flex: 1;
  }
  .permission-grid {
    grid-template-columns: 1fr;
  }
  .menu-tree {
    display: flex;
    overflow: auto;
    border-right: 0;
    border-bottom: 1px solid var(--color-border);
    padding: 8px;
  }
  .menu-tree > h4 {
    display: none;
  }
  .menu-item {
    flex-shrink: 0;
  }
  .action-editor {
    padding: 14px;
  }
  .action-line {
    gap: 10px;
  }
  .role-layout {
    gap: 12px;
  }
  .actions {
    flex-wrap: wrap;
  }
  .panel-footer > .actions {
    margin-left: auto;
  }
  .access-dialog {
    max-height: calc(100dvh - 20px);
  }
  .credential-value {
    flex-wrap: wrap;
  }
  input[type="checkbox"] {
    width: 17px;
    height: 17px;
  }
  .check-label {
    min-height: 32px;
  }
}
</style>
