<script setup lang="ts">
import ResourcePager from "../../components/ui/ResourcePager.vue";
import {usePagedResources,includeSelected} from "../../composables/usePagedResources";
import { useIdentityRequestScope } from "../../composables/useAuthorizedRequestScope";
import { usePermission, usePermissions } from "../../composables/usePermission";
import { useResourcePermissionQueries } from "../../composables/useResourcePermissionQueries";
import { usePermissionStore } from "../../stores/permissions";
import { useSessionStore } from "../../stores/session";
import type { PermissionQuery } from "../../api/access";

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
import {
  kubeAPI,
  kubeError,
  type KubeConnection,
  type KubeConnectionInput,
  type KubeCredential,
  type KubeGrant,
  type KubeGrantInput,
  type KubeExecutor,
  type KubeReceipt,
} from "../../api/kubernetes";
import { http } from "../../api/http";
import type { Server } from "../../api/servers";
import type { Project } from "../../api/projects";
import type { Credential } from "../../api/credentials";
import { useKubeMutation } from "../../composables/useKubeMutation";
import { useConfirm } from "../../composables/useConfirm";
import AppButton from "../../components/ui/AppButton.vue";
import FormField from "../../components/ui/FormField.vue";
import KubeDialog from "../../components/kubernetes/KubeDialog.vue";
import KubeReadBack from "../../components/kubernetes/KubeReadBack.vue";
import KubeReleaseSettings from "../../components/kubernetes/KubeReleaseSettings.vue";
import KubePassword from "../../components/kubernetes/KubePassword.vue";
import "../../styles/kubernetes.css";
const { t } = useI18n(),
  confirm = useConfirm(),
  gate = useKubeMutation(() => scope.requestOptions);
const { busy, error, uncertain } = gate;
const readBack = ref(false);
watch(uncertain, () => {
  readBack.value = false;
});
async function readOriginal() {
  readBack.value = false;
  await load();
  if (selectedId.value) await loadGrants();
  if (!loadError.value && !grantsError.value && alive) readBack.value = true;
}
function acknowledge() {
  connectionOpen.value = false;
  grantOpen.value = false;
  gate.reset();
  readBack.value = false;
}
const rows = ref<KubeConnection[]>([]),
  credentials = ref<KubeCredential[]>([]),
  servers = ref<Server[]>([]),
  projects = ref<Project[]>([]),
  registry = ref<Credential[]>([]);
const loading = ref(false),
  loadError = ref(""),
  selectedId = ref(""),
  grants = ref<KubeGrant[]>([]),
  grantsLoading = ref(false),
  grantsError = ref("");
const selected = computed(() =>
  rows.value.find((row) => row.id === selectedId.value),
);
const editing = ref<KubeConnection>(),
  connectionOpen = ref(false),
  grantOpen = ref(false),
  editingGrant = ref<KubeGrant>();
const draft = reactive<KubeConnectionInput>(blank()),
  local = ref(true),
  ssh = ref<Array<{ serverId: string; hostKeySHA256: string }>>([]);
const grant = reactive<KubeGrantInput>(blankGrant()),
  containerNames = ref(""),
  executorIndex = ref("");
const probeGrant = ref(""),
  probeContainer = ref(""),
  probeResult = ref<KubeReceipt>();
const probeChoices = computed(() =>
  grants.value.filter((g) => g.enabled && can("project.read", g.projectId)),
);
const probeContainers = computed(
  () =>
    probeChoices.value.find((g) => g.id === probeGrant.value)?.containers ?? [],
);
let life = new AbortController(),
  loadController = new AbortController(),
  grantController = new AbortController(),
  seq = 0,
  grantSeq = 0,
  alive = true;
const permissions = usePermissionStore(),
  session = useSessionStore();
const scope = useIdentityRequestScope(() => {
  alive = false;
  seq++;
  grantSeq++;
  life.abort();
  loadController.abort();
  grantController.abort();
  gate.reset();
  rows.value = [];
  credentials.value = [];
  servers.value = [];
  projects.value = [];
  registry.value = [];
  grants.value = [];
  selectedId.value = "";
  connectionOpen.value = false;
  grantOpen.value = false;
  Object.assign(draft, blank());
  Object.assign(grant, blankGrant());
  probeResult.value = undefined;
  loadError.value = "";
  grantsError.value = "";
});
function can(action: string, id = "", create = false) {
  return (
    scope.active() &&
    permissions.allowed(permissions.activeTeam, {
      action,
      resourceId: id,
      create,
    })
  );
}
const createPermission = usePermission({
  action: "kubernetes.create",
  resourceId: "",
  create: true,
});
const connectionQueries = computed<PermissionQuery[]>(() => [
  editing.value
    ? { action: "kubernetes.edit", resourceId: editing.value.id }
    : { action: "kubernetes.create", resourceId: "", create: true },
  { action: "kubernetes_credential.use", resourceId: draft.credentialId },
  ...(draft.profile.registryCredentialId
    ? [
        {
          action: "credential.use",
          resourceId: draft.profile.registryCredentialId,
        },
      ]
    : []),
  ...ssh.value.map((e) => ({ action: "server.use", resourceId: e.serverId })),
]);
const connectionPermission = usePermissions(connectionQueries);
const connectionBasePermission = usePermission(() =>
  editing.value
    ? { action: "kubernetes.edit", resourceId: editing.value.id }
    : { action: "kubernetes.create", resourceId: "", create: true },
);
watch(
  () => [
    connectionBasePermission.allowed.value,
    connectionBasePermission.pending.value,
  ],
  ([ok]) => {
    if (
      connectionOpen.value &&
      !ok &&
      !connectionBasePermission.pending.value
    ) {
      connectionOpen.value = false;
      gate.reset();
      Object.assign(draft, blank());
    }
  },
);
const probeTarget = computed(() =>
  grants.value.find((g) => g.id === probeGrant.value),
);
const probeQueries = computed<PermissionQuery[]>(() => {
  const target = probeTarget.value,
    connection = selected.value;
  if (!target || !connection) return [];
  return [
    { action: "kubernetes.use", resourceId: connection.id },
    {
      action: "kubernetes_credential.use",
      resourceId: connection.credentialId,
    },
    { action: "project.read", resourceId: target.projectId },
    {
      action: "kubernetes.deploy",
      resourceId: connection.id,
      environmentId: target.environmentId ?? "",
    },
    ...(target.executor.kind === "ssh"
      ? [{ action: "server.use", resourceId: target.executor.serverId ?? "" }]
      : []),
  ];
});
const probePermission = usePermissions(probeQueries);
const mayProbe = computed(
  () =>
    scope.active() &&
    probePermission.allowed.value &&
    (probeTarget.value?.executor.kind !== "local" || mayLocal.value),
);
const selectedEdit = usePermission(() => ({
  action: "kubernetes.edit",
  resourceId: selectedId.value,
}));
const selectedRead = usePermission(() => ({
  action: "kubernetes.read",
  resourceId: selectedId.value,
}));
const grantPermission = usePermission(() => ({
  action: "project.read",
  resourceId: grant.projectId,
}));
const rowPages=usePagedResources(rows),grantPages=usePagedResources(grants),credentialPages=usePagedResources(credentials),serverPages=usePagedResources(servers),projectPages=usePagedResources(projects),registryPages=usePagedResources(registry);
const credentialChoices=computed(()=>includeSelected(credentialPages.items.value,credentials.value,[draft.credentialId]));
const serverChoices=computed(()=>includeSelected(serverPages.items.value,servers.value,ssh.value.map(host=>host.serverId)));
const projectChoices=computed(()=>includeSelected(projectPages.items.value,projects.value,[grant.projectId]));
const registryChoices=computed(()=>includeSelected(registryPages.items.value,registry.value,[draft.profile.registryCredentialId??'']));
useResourcePermissionQueries(() =>
  rowPages.items.value.flatMap((row) =>
    [
      "kubernetes.read",
      "kubernetes.edit",
      "kubernetes.delete",
      "kubernetes.use",
    ].map((action) => ({ action, resourceId: row.id })),
  ),
);
useResourcePermissionQueries(() => [
  ...grantPages.items.value.map((g) => ({
    action: "project.read",
    resourceId: g.projectId,
  })),
  ...credentialChoices.value.flatMap((c) =>
    ["kubernetes_credential.read", "kubernetes_credential.use"].map(
      (action) => ({ action, resourceId: c.id }),
    ),
  ),
  ...serverChoices.value.map((c) => ({ action: "server.inspect", resourceId: c.id })),
  ...projectChoices.value.map((c) => ({ action: "project.read", resourceId: c.id })),
  ...registryChoices.value.map((c) => ({
    action: "credential.metadata",
    resourceId: c.id,
  })),
]);
const visibleCredentials = computed(() =>
  credentialChoices.value.filter((c) => can("kubernetes_credential.read", c.id)),
);
const visibleServers = computed(() =>
  serverChoices.value.filter((c) => can("server.inspect", c.id)),
);
const visibleProjects = computed(() =>
  projectChoices.value.filter((c) => can("project.read", c.id)),
);
const visibleRegistry = computed(() =>
  registryChoices.value.filter((c) => can("credential.metadata", c.id)),
);
const visibleGrants = computed(() =>
  grantPages.items.value.filter((g) => can("project.read", g.projectId)),
);
const visibleRows = computed(() =>
  rowPages.items.value.filter((row) => can("kubernetes.read", row.id)),
);
const mayLocal = computed(
  () =>
    session.user?.multiTenant === false || session.user?.instanceAdmin === true,
);
watch(
  () => [selectedRead.allowed.value, selectedRead.pending.value],
  ([ok]) => {
    if (ok && selectedId.value && !grantsLoading.value && !grants.value.length)
      void loadGrants();
    if (selectedId.value && !ok && !selectedRead.pending.value) {
      selectedId.value = "";
      gate.reset();
      grants.value = [];
      probeResult.value = undefined;
    }
  },
);
watch(
  () => [mayProbe.value, probePermission.pending.value],
  ([ok]) => {
    if (!ok) {
      probeResult.value = undefined;
      if (!probePermission.pending.value) gate.cancel();
    }
  },
);
watch(
  () => [selectedEdit.allowed.value, selectedEdit.pending.value],
  ([ok]) => {
    if (grantOpen.value && !ok && !selectedEdit.pending.value) {
      grantOpen.value = false;
      gate.reset();
      Object.assign(grant, blankGrant());
    }
  },
);
function blank(): KubeConnectionInput {
  return {
    name: "",
    credentialId: "",
    enabled: true,
    profile: {
      protocol: "kubectl-sh-v1",
      image: "",
      architecture: "amd64",
      kubectlPath: "/usr/local/bin/kubectl",
      uid: 1000,
      network: "bridge",
      registryCredentialId: "",
    },
    executors: [],
  };
}
function blankGrant(): KubeGrantInput {
  return {
    projectId: "",
    environment: "",
    namespace: "",
    deployment: "",
    containers: [],
    executor: { kind: "local" },
    requireApproval: true,
    enabled: true,
  };
}
function executorLabel(e: KubeExecutor) {
  return e.kind === "local"
    ? t("kubernetes.local")
    : "SSH · " +
        (servers.value.find((s) => s.id === e.serverId)?.name ?? e.serverId);
}
async function load() {
  if (!scope.active()) return;
  loadController.abort();
  loadController = new AbortController();
  const signal = loadController.signal,
    n = ++seq;
  loading.value = true;
  loadError.value = "";
  try {
    const [data, creds, hosts, projs, vault] = await scope.run(() =>
      Promise.all([
        kubeAPI.connections(signal, scope.requestOptions),
        kubeAPI.credentials(signal, scope.requestOptions),
        http.get<{ items: Server[] }>("/api/servers", {
          ...scope.requestOptions,
          signal,
        }),
        http.get<Project[]>("/api/projects", {
          ...scope.requestOptions,
          signal,
        }),
        http.get<Credential[]>("/api/credentials", {
          ...scope.requestOptions,
          signal,
        }),
      ]),
    );
    if (!alive || signal.aborted || n !== seq) return;
    rows.value = data;
    credentials.value = creds;
    servers.value = hosts.items ?? [];
    projects.value = projs;
    registry.value = vault.filter((c) => c.type === "registry");
    if (selectedId.value && !data.some((r) => r.id === selectedId.value))
      selectedId.value = "";
  } catch (e) {
    if (alive && !signal.aborted && n === seq) loadError.value = kubeError(e);
  } finally {
    if (alive && n === seq) loading.value = false;
  }
}
async function loadGrants() {
  grantController.abort();
  grantController = new AbortController();
  const signal = grantController.signal,
    n = ++grantSeq,
    key = selectedId.value;
  grants.value = [];
  grantsError.value = "";
  probeGrant.value = "";
  probeContainer.value = "";
  probeResult.value = undefined;
  if (!key || !can("kubernetes.read", key)) return;
  grantsLoading.value = true;
  try {
    const data = await scope.run(() =>
      kubeAPI.grants(key, signal, scope.requestOptions),
    );
    if (alive && !signal.aborted && n === grantSeq) grants.value = data;
  } catch (e) {
    if (alive && !signal.aborted && n === grantSeq)
      grantsError.value = kubeError(e);
  } finally {
    if (alive && n === grantSeq) grantsLoading.value = false;
  }
}
watch(selectedId, () => {
  gate.reset();
  grantOpen.value = false;
  void loadGrants();
});
watch(probeGrant, () => {
  probeContainer.value = "";
  probeResult.value = undefined;
});
watch(probeContainer, () => {
  probeResult.value = undefined;
});
function edit(row?: KubeConnection) {
  if (row ? !can("kubernetes.edit", row.id) : !createPermission.allowed.value)
    return;
  editing.value = row;
  Object.assign(
    draft,
    row
      ? {
          name: row.name,
          credentialId: row.credentialId,
          enabled: row.enabled,
          profile: { ...row.profile },
          executors: row.executors.map((e) => ({ ...e })),
        }
      : blank(),
  );
  local.value = row
    ? row.executors.some((e) => e.kind === "local")
    : mayLocal.value;
  ssh.value = row
    ? row.executors
        .filter((e) => e.kind === "ssh")
        .map((e) => ({
          serverId: e.serverId ?? "",
          hostKeySHA256: e.hostKeySHA256 ?? "",
        }))
    : [];
  connectionOpen.value = true;
}
function saveConnection() {
  if (
    !connectionOpen.value ||
    !scope.active() ||
    !connectionPermission.allowed.value ||
    (local.value && !mayLocal.value)
  )
    return;
  gate.execute(async (signal) => {
    if (
      !connectionOpen.value ||
      !scope.active() ||
      !connectionPermission.allowed.value ||
      (local.value && !mayLocal.value)
    )
      return;
    const input: KubeConnectionInput = {
      ...draft,
      profile: { ...draft.profile },
      executors: [
        ...(local.value ? [{ kind: "local" as const }] : []),
        ...ssh.value.map((e) => ({ kind: "ssh" as const, ...e })),
      ],
    };
    const row = await scope.run(() =>
      kubeAPI.saveConnection(
        editing.value?.id,
        editing.value?.revision ?? 0,
        input,
        signal,
        scope.requestOptions,
      ),
    );
    if (signal.aborted) return;
    connectionOpen.value = false;
    await load();
    if (!signal.aborted) selectedId.value = row.id;
  });
}
async function remove(row: KubeConnection) {
  if (!can("kubernetes.delete", row.id)) return;
  if (
    !(await confirm.open({
      title: t("kubernetes.delete"),
      body: t("kubernetes.deleteBody", { name: row.name }),
      confirmLabel: t("kubernetes.delete"),
      variant: "danger",
    })) ||
    !alive ||
    !can("kubernetes.delete", row.id)
  )
    return;
  gate.execute(async (signal) => {
    if (!can("kubernetes.delete", row.id)) return;
    await scope.run(() =>
      kubeAPI.deleteConnection(row, signal, scope.requestOptions),
    );
    if (!signal.aborted) await load();
  });
}
function editGrant(row?: KubeGrant) {
  if (!scope.active() || !selectedEdit.allowed.value) return;
  editingGrant.value = row;
  Object.assign(
    grant,
    row
      ? {
          ...row,
          containers: [...row.containers],
          executor: { ...row.executor },
        }
      : blankGrant(),
  );
  containerNames.value = row?.containers.join(", ") ?? "";
  executorIndex.value = row
    ? String(
        selected.value?.executors.findIndex(
          (e) => JSON.stringify(e) === JSON.stringify(row.executor),
        ) ?? -1,
      )
    : "";
  grantOpen.value = true;
}
function saveGrant() {
  const connection = selected.value;
  const executor = connection?.executors[Number(executorIndex.value)];
  if (
    !grantOpen.value ||
    !connection ||
    executorIndex.value === "" ||
    !executor ||
    !scope.active() ||
    !selectedEdit.allowed.value ||
    !grantPermission.allowed.value
  )
    return;
  gate.execute(async (signal) => {
    if (
      !grantOpen.value ||
      selectedId.value !== connection.id ||
      !scope.active() ||
      !selectedEdit.allowed.value ||
      !grantPermission.allowed.value
    )
      return;
    const input: KubeGrantInput = {
      projectId: grant.projectId,
      environment: grant.environment.trim(),
      namespace: grant.namespace.trim(),
      deployment: grant.deployment.trim(),
      containers: containerNames.value
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean),
      executor: { ...executor },
      requireApproval: grant.requireApproval,
      enabled: grant.enabled,
    };
    await scope.run(() =>
      kubeAPI.saveGrant(
        connection.id,
        editingGrant.value?.id,
        editingGrant.value?.revision ?? 0,
        input,
        signal,
        scope.requestOptions,
      ),
    );
    if (signal.aborted) return;
    grantOpen.value = false;
    await loadGrants();
  });
}
async function removeGrant(row: KubeGrant) {
  const key = row.connectionId;
  const canRemove = () =>
    scope.active() &&
    selectedId.value === key &&
    selectedEdit.allowed.value &&
    can("project.read", row.projectId);
  if (!canRemove()) return;
  if (
    !(await confirm.open({
      title: t("kubernetes.delete"),
      body: t("kubernetes.deleteBody", {
        name: row.namespace + "/" + row.deployment,
      }),
      confirmLabel: t("kubernetes.delete"),
      variant: "danger",
    })) ||
    !alive ||
    !canRemove()
  )
    return;
  gate.execute(async (signal) => {
    if (!canRemove()) return;
    await scope.run(() =>
      kubeAPI.deleteGrant(key, row, signal, scope.requestOptions),
    );
    if (!signal.aborted) await loadGrants();
  });
}
function probe() {
  const key = selectedId.value,
    g = probeGrant.value,
    c = probeContainer.value;
  if (!key || !g || !probeContainers.value.includes(c) || !mayProbe.value)
    return;
  gate.execute(async (signal) => {
    if (
      !mayProbe.value ||
      key !== selectedId.value ||
      g !== probeGrant.value ||
      c !== probeContainer.value
    )
      return;
    const result = await scope.run(() =>
      kubeAPI.probe(key, g, c, signal, scope.requestOptions),
    );
    if (
      !signal.aborted &&
      key === selectedId.value &&
      g === probeGrant.value &&
      c === probeContainer.value
    )
      probeResult.value = result;
  });
}
function close() {
  if (busy.value) return;
  connectionOpen.value = false;
  grantOpen.value = false;
  gate.cancel();
}
onBeforeRouteLeave(async () => {
  if (busy.value) return false;
  if (connectionOpen.value || grantOpen.value) {
    const reopenConnection = connectionOpen.value,
      reopenGrant = grantOpen.value;
    connectionOpen.value = false;
    grantOpen.value = false;
    gate.cancel();
    await nextTick();
    const leave = await confirm.open({
      title: t("kubernetes.unsaved"),
      body: t("kubernetes.unsavedBody"),
      confirmLabel: t("kubernetes.discard"),
    });
    if (!leave) {
      connectionOpen.value = reopenConnection;
      grantOpen.value = reopenGrant;
      return false;
    }
  }
  return true;
});
onMounted(load);
onBeforeUnmount(() => {
  alive = false;
  life.abort();
  loadController.abort();
  grantController.abort();
});
</script>
<template>
  <section class="settings-page kube-surface">
    <header class="kube-head">
      <div>
        <h2>Kubernetes</h2>
        <p class="kube-muted">{{ t("kubernetes.connectionsHint") }}</p>
      </div>
      <div class="kube-actions">
        <AppButton :loading="loading" :disabled="busy" @click="load">{{
          t("common.refresh")
        }}</AppButton
        ><AppButton
          variant="primary"
          :disabled="busy || uncertain"
          v-if="createPermission.allowed.value"
          @click="edit()"
          >{{ t("kubernetes.addConnection") }}</AppButton
        >
      </div>
    </header>
    <p v-if="loadError" role="alert" class="kube-error">{{ t(loadError) }}</p>
    <p
      v-if="error && !connectionOpen && !grantOpen"
      role="alert"
      class="kube-error"
    >
      {{ t(error) }}
    </p>
    <div v-if="!loading && !visibleRows.length && !loadError" class="kube-card">
      <h3>{{ t("kubernetes.emptyConnections") }}</h3>
      <p class="kube-muted">{{ t("kubernetes.setupHint") }}</p>
      <RouterLink to="/settings/vault">{{
        t("kubernetes.manageCredentials")
      }}</RouterLink>
    </div>
    <ResourcePager :page="rowPages.page.value" :pages="rowPages.pages.value" @change="rowPages.select" />
    <article v-for="row in visibleRows" :key="row.id" class="kube-card">
      <div class="kube-row">
        <div class="kube-row-main">
          <strong>{{ row.name }}</strong>
          <span class="kube-status">{{
            t(row.enabled ? "kubernetes.enabled" : "kubernetes.disabled")
          }}</span>
          <p class="kube-muted">{{ row.context }} · {{ row.server }}</p>
          <p class="kube-muted">
            {{ row.executors.map(executorLabel).join(" · ") }}
          </p>
        </div>
        <div class="kube-actions">
          <AppButton
            :disabled="busy || uncertain"
            @click="selectedId = selectedId === row.id ? '' : row.id"
            >{{ t("kubernetes.grants") }}</AppButton
          ><AppButton
            :disabled="busy || uncertain"
            v-if="can('kubernetes.edit', row.id)"
            @click="edit(row)"
            >{{ t("kubernetes.edit") }}</AppButton
          ><AppButton
            variant="ghost"
            :disabled="busy || uncertain"
            v-if="can('kubernetes.delete', row.id)"
            @click="remove(row)"
            >{{ t("kubernetes.delete") }}</AppButton
          >
        </div>
      </div>
      <details class="kube-details">
        <summary>{{ t("kubernetes.identity") }}</summary>
        <dl class="kube-detail">
          <dt>{{ t("kubernetes.credential") }}</dt>
          <dd>{{ row.credentialId }}</dd>
          <dt>{{ t("kubernetes.toolImage") }}</dt>
          <dd>{{ row.profile.image }}</dd>
          <dt>{{ t("kubernetes.profile") }}</dt>
          <dd>{{ row.profileFingerprint }}</dd>
        </dl>
        <p class="kube-muted">{{ t("kubernetes.profileHint") }}</p>
      </details>
      <section v-if="selectedId === row.id" class="kube-section">
        <div class="kube-head">
          <h3>{{ t("kubernetes.grants") }}</h3>
          <AppButton
            :disabled="busy || uncertain"
            v-if="selectedEdit.allowed.value"
            @click="editGrant()"
            >{{ t("kubernetes.addGrant") }}</AppButton
          >
        </div>
        <p v-if="grantsLoading" role="status">{{ t("kubernetes.loading") }}</p>
        <p v-if="grantsError" class="kube-error" role="alert">
          {{ t(grantsError) }}
          <AppButton @click="loadGrants">{{ t("common.refresh") }}</AppButton>
        </p>
        <p
          v-if="!grantsLoading && !grantsError && !grants.length"
          class="kube-muted"
        >
          {{ t("kubernetes.emptyGrants") }}
        </p>
        <ResourcePager :page="grantPages.page.value" :pages="grantPages.pages.value" @change="grantPages.select" />
        <div v-for="g in visibleGrants" :key="g.id" class="kube-row">
          <div class="kube-row-main">
            <strong
              >{{
                projects.find((p) => p.id === g.projectId)?.name ?? g.projectId
              }}
              · {{ g.environment }}</strong
            >
            <p>
              {{ g.namespace }} / {{ g.deployment }} ·
              {{ g.containers.join(", ") }}
            </p>
            <p class="kube-muted">
              {{ executorLabel(g.executor) }} ·
              {{
                t(
                  g.requireApproval
                    ? "kubernetes.approvalRequired"
                    : "kubernetes.approvalOptional",
                )
              }}
              ·
              {{ t(g.enabled ? "kubernetes.enabled" : "kubernetes.disabled") }}
            </p>
          </div>
          <div class="kube-actions">
            <AppButton
              :disabled="busy || uncertain"
              v-if="selectedEdit.allowed.value"
              @click="editGrant(g)"
              >{{ t("kubernetes.edit") }}</AppButton
            ><AppButton
              variant="ghost"
              :disabled="busy || uncertain"
              v-if="selectedEdit.allowed.value"
              @click="removeGrant(g)"
              >{{ t("kubernetes.delete") }}</AppButton
            >
          </div>
        </div>
        <div v-if="grants.length" class="kube-form kube-section">
          <h3>{{ t("kubernetes.probe") }}</h3>
          <p class="kube-muted">{{ t("kubernetes.probeHint") }}</p>
          <div class="kube-grid">
            <FormField
              :label="t('kubernetes.target')"
              :field-id="'kp-grant-' + row.id"
              ><select
                :id="'kp-grant-' + row.id"
                v-model="probeGrant"
                :disabled="busy"
              >
                <option value="">{{ t("kubernetes.select") }}</option>
                <option v-for="g in probeChoices" :key="g.id" :value="g.id">
                  {{ g.environment }} · {{ g.namespace }}/{{ g.deployment }} ·
                  {{ executorLabel(g.executor) }}
                </option>
              </select></FormField
            ><FormField
              :label="t('kubernetes.container')"
              :field-id="'kp-container-' + row.id"
              ><select
                :id="'kp-container-' + row.id"
                v-model="probeContainer"
                :disabled="busy"
              >
                <option value="">{{ t("kubernetes.select") }}</option>
                <option v-for="c in probeContainers" :key="c">{{ c }}</option>
              </select></FormField
            >
          </div>
          <div>
            <AppButton
              :disabled="
                !probeGrant || !probeContainer || uncertain || !mayProbe
              "
              :loading="busy"
              @click="probe"
              >{{ t("kubernetes.probe") }}</AppButton
            >
          </div>
          <p v-if="probeResult" class="kube-note" role="status">
            {{ t("kubernetes.probeReady") }} ·
            {{ probeResult.availableReplicas }} /
            {{ probeResult.desiredReplicas }} {{ t("kubernetes.replicas") }}
          </p>
        </div>
      </section>
    </article>
    <KubeDialog
      :open="connectionOpen"
      :title="
        t(editing ? 'kubernetes.editConnection' : 'kubernetes.addConnection')
      "
      :busy="busy"
      @close="close"
      ><form class="kube-form" @submit.prevent="saveConnection">
        <KubeReadBack
          v-if="uncertain"
          :loaded="readBack"
          :loading="loading || grantsLoading"
          :records="[...visibleRows, ...visibleGrants]"
          @read="readOriginal"
          @done="acknowledge"
        />
        <FormField :label="t('kubernetes.name')" field-id="kn-name"
          ><input
            id="kn-name"
            v-model="draft.name"
            required
            :disabled="busy" /></FormField
        ><FormField :label="t('kubernetes.credential')" field-id="kn-credential"
          ><select
            id="kn-credential"
            v-model="draft.credentialId"
            required
            :disabled="busy"
          >
            <option value="">{{ t("kubernetes.select") }}</option>
            <option
              v-for="c in visibleCredentials"
              :key="c.id"
              :value="c.id"
              :disabled="!c.enabled"
            >
              {{ c.name }} · {{ c.context }}
            </option>
          </select><ResourcePager :page="credentialPages.page.value" :pages="credentialPages.pages.value" @change="credentialPages.select" /></FormField
        ><RouterLink to="/settings/vault">{{
          t("kubernetes.manageCredentials")
        }}</RouterLink>
        <fieldset :disabled="busy" class="kube-form">
          <legend>{{ t("kubernetes.profile") }}</legend>
          <p class="kube-muted">{{ t("kubernetes.profileHint") }}</p>
          <FormField :label="t('kubernetes.toolImage')" field-id="kn-image"
            ><input
              id="kn-image"
              v-model="draft.profile.image"
              placeholder="registry.example/tools/kubectl@sha256:…"
              required
          /></FormField>
          <div class="kube-grid">
            <FormField :label="t('kubernetes.architecture')" field-id="kn-arch"
              ><select id="kn-arch" v-model="draft.profile.architecture">
                <option>amd64</option>
                <option>arm64</option>
              </select></FormField
            ><FormField label="UID" field-id="kn-uid"
              ><input
                id="kn-uid"
                v-model.number="draft.profile.uid"
                type="number"
                min="1"
                step="1"
                required
            /></FormField>
          </div>
          <FormField :label="t('kubernetes.kubectlPath')" field-id="kn-path"
            ><input
              id="kn-path"
              v-model="draft.profile.kubectlPath"
              required /></FormField
          ><FormField
            :label="t('kubernetes.registryCredential')"
            field-id="kn-registry"
            ><select
              id="kn-registry"
              v-model="draft.profile.registryCredentialId"
            >
              <option value="">{{ t("kubernetes.none") }}</option>
              <option v-for="c in visibleRegistry" :key="c.id" :value="c.id">
                {{ c.name }}
              </option>
            </select><ResourcePager :page="registryPages.page.value" :pages="registryPages.pages.value" @change="registryPages.select" /></FormField
          >
        </fieldset>
        <fieldset :disabled="busy" class="kube-form">
          <legend>{{ t("kubernetes.executors") }}</legend>
          <label class="kube-check"
            ><input v-model="local" type="checkbox" :disabled="!mayLocal" />{{
              t("kubernetes.local")
            }}</label
          >
          <div v-for="(host, i) in ssh" :key="i" class="kube-card kube-form">
            <FormField
              :label="t('kubernetes.sshServer')"
              :field-id="'kn-ssh-' + i"
              ><select :id="'kn-ssh-' + i" v-model="host.serverId" required>
                <option value="">{{ t("kubernetes.select") }}</option>
                <option
                  v-for="srv in visibleServers"
                  :key="srv.id"
                  :value="srv.id"
                >
                  {{ srv.name }} · {{ srv.host }}
                </option>
              </select><ResourcePager :page="serverPages.page.value" :pages="serverPages.pages.value" @change="serverPages.select" /></FormField
            ><FormField
              :label="t('kubernetes.hostKey')"
              :field-id="'kn-pin-' + i"
              :hint="t('kubernetes.hostKeyHint')"
              ><input
                :id="'kn-pin-' + i"
                v-model="host.hostKeySHA256"
                placeholder="SHA256:…"
                required /></FormField
            ><AppButton
              variant="ghost"
              @click="
                connectionBasePermission.allowed.value && ssh.splice(i, 1)
              "
              >{{ t("kubernetes.remove") }}</AppButton
            >
          </div>
          <div>
            <AppButton
              @click="
                connectionBasePermission.allowed.value &&
                ssh.push({ serverId: '', hostKeySHA256: '' })
              "
              >{{ t("kubernetes.addSSH") }}</AppButton
            >
          </div>
        </fieldset>
        <label class="kube-check"
          ><input v-model="draft.enabled" type="checkbox" :disabled="busy" />{{
            t("kubernetes.enabled")
          }}</label
        >
        <p v-if="error" role="alert" class="kube-error">{{ t(error) }}</p>
        <footer class="kube-actions">
          <AppButton :disabled="busy" @click="close">{{
            t("kubernetes.cancel")
          }}</AppButton
          ><AppButton
            variant="primary"
            type="submit"
            :disabled="
              uncertain ||
              !connectionPermission.allowed.value ||
              (!local && !ssh.length) ||
              (local && !mayLocal)
            "
            :loading="busy"
            >{{ t("kubernetes.save") }}</AppButton
          >
        </footer>
      </form></KubeDialog
    >
    <KubeDialog
      :open="grantOpen"
      :title="t('kubernetes.grantTitle')"
      :busy="busy"
      @close="close"
      ><form class="kube-form" @submit.prevent="saveGrant">
        <KubeReadBack
          v-if="uncertain"
          :loaded="readBack"
          :loading="loading || grantsLoading"
          :records="[...visibleRows, ...visibleGrants]"
          @read="readOriginal"
          @done="acknowledge"
        />
        <p class="kube-note">
          {{ selected?.name }} · {{ t("kubernetes.grantHint") }}
        </p>
        <fieldset :disabled="busy" class="kube-form">
          <FormField :label="t('kubernetes.project')" field-id="kg-project"
            ><select id="kg-project" v-model="grant.projectId" required>
              <option value="">{{ t("kubernetes.select") }}</option>
              <option v-for="p in visibleProjects" :key="p.id" :value="p.id">
                {{ p.name }}
              </option>
            </select><ResourcePager :page="projectPages.page.value" :pages="projectPages.pages.value" @change="projectPages.select" /></FormField
          >
          <div class="kube-grid">
            <FormField
              v-for="key in ['environment', 'namespace', 'deployment'] as const"
              :key="key"
              :label="t('kubernetes.' + key)"
              :field-id="'kg-' + key"
              ><input :id="'kg-' + key" v-model="grant[key]" required
            /></FormField>
          </div>
          <FormField
            :label="t('kubernetes.containers')"
            field-id="kg-containers"
            :hint="t('kubernetes.containersHint')"
            ><input
              id="kg-containers"
              v-model="containerNames"
              required /></FormField
          ><FormField :label="t('kubernetes.executor')" field-id="kg-executor"
            ><select id="kg-executor" v-model="executorIndex" required>
              <option value="">{{ t("kubernetes.select") }}</option>
              <option
                v-for="(e, i) in selected?.executors ?? []"
                :key="i"
                :value="String(i)"
              >
                {{ executorLabel(e) }}
              </option>
            </select></FormField
          ><label class="kube-check"
            ><input v-model="grant.requireApproval" type="checkbox" />{{
              t("kubernetes.approvalRequired")
            }}</label
          ><label class="kube-check"
            ><input v-model="grant.enabled" type="checkbox" />{{
              t("kubernetes.enabled")
            }}</label
          >
        </fieldset>
        <p v-if="error" class="kube-error" role="alert">{{ t(error) }}</p>
        <footer class="kube-actions">
          <AppButton :disabled="busy" @click="close">{{
            t("kubernetes.cancel")
          }}</AppButton
          ><AppButton
            variant="primary"
            type="submit"
            :disabled="uncertain || !selectedEdit.allowed.value || !grantPermission.allowed.value"
            :loading="busy"
            >{{ t("kubernetes.save") }}</AppButton
          >
        </footer>
      </form></KubeDialog
    >
    <KubeReadBack
      v-if="uncertain && !connectionOpen && !grantOpen"
      :loaded="readBack"
      :loading="loading || grantsLoading"
      :records="[...visibleRows, ...visibleGrants]"
      @read="readOriginal"
      @done="acknowledge"
    />
    <KubeReleaseSettings
      v-if="selected"
      :key="selected.id"
      :connection="selected"
      :projects="projects"
    /><KubePassword :state="gate" />
  </section>
</template>
