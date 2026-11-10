<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  kubeAPI,
  kubeError,
  type KubeConnection,
  type KubeReleaseProfile,
  type KubeReleaseGrant,
  type KubeReleaseGrantInput,
  type KubeReleaseAccess,
} from "../../api/kubernetes";
import { HttpError } from "../../api/http";
import type { Project } from "../../api/projects";
import { useKubeMutation } from "../../composables/useKubeMutation";
import { useConfirm } from "../../composables/useConfirm";
import AppButton from "../ui/AppButton.vue";
import FormField from "../ui/FormField.vue";
import KubePassword from "./KubePassword.vue";
import KubeReadBack from "./KubeReadBack.vue";
const props = defineProps<{
  connection: KubeConnection;
  projects: Project[];
}>();
const { t } = useI18n(),
  gate = useKubeMutation(),
  confirm = useConfirm();
const { busy, error, uncertain } = gate;
const profile = ref<KubeReleaseProfile>(),
  grants = ref<KubeReleaseGrant[]>([]),
  loadError = ref(""),
  loading = ref(false),
  editing = ref<KubeReleaseGrant>(),
  open = ref(false),
  readBack = ref(false);
const grant = reactive<KubeReleaseGrantInput>(blank());
const actions = [
  "get",
  "create",
  "patch",
  "update",
  "delete",
  "rollback",
  "scale",
  "related-pods",
  "related-replicasets",
  "related-endpointslices",
  "related-hpas",
  "related-controllerrevisions",
  "related-jobs",
  "probe",
  "helm-storage",
  "helm-hook",
  "coredns",
  "bind",
  "escalate",
  "impersonate",
  "namespace-security",
  "watch",
];
let request = new AbortController(),
  generation = 0;
function blank(): KubeReleaseGrantInput {
  return {
    projectId: "",
    environment: "",
    executor: { kind: "local" },
    resources: [],
    requireApproval: true,
    enabled: true,
  };
}
// Serialize only writable fields. A read DTO is never a write DTO.
function grantInput(value: KubeReleaseGrantInput): KubeReleaseGrantInput {
 return {projectId:value.projectId,environment:value.environment,
 executor:value.executor.kind==='local' ? {kind:'local'} : {kind:'ssh',serverId:value.executor.serverId},
 resources:value.resources.map(scope=>({resource:{group:scope.resource.group,version:scope.resource.version,resource:scope.resource.resource,kind:scope.resource.kind,namespace:scope.resource.namespace,name:scope.resource.name},actions:[...scope.actions],containers:[...(scope.containers??[])],fields:[...(scope.fields??[])]})),
 requireApproval:value.requireApproval,enabled:value.enabled};
}
function resetGrant(row?: KubeReleaseGrantInput) {
 for (const key of Object.keys(grant)) delete (grant as unknown as Record<string,unknown>)[key];
 Object.assign(grant,row ? grantInput(row) : blank());
}
function defaultProfile(): KubeReleaseProfile {
  return {
    connectionId: props.connection.id,
    revision: 0,
    enabled: true,
    profile: {
      runtime: { ...props.connection.profile, protocol: "kube-gateway-v2" },
      helperPath: "/usr/bin/pipewright-kube-tool",
      helmPath: "/usr/bin/helm",
      helmVersion: "v4.3.1",
    },
  };
}
async function load() {
  request.abort();
  request = new AbortController();
  const signal = request.signal,
    n = ++generation;
  loading.value = true;
  loadError.value = "";
  try {
    const rows = await kubeAPI.releaseGrants(props.connection.id, signal);
    let tool: KubeReleaseProfile;
    try {
      tool = await kubeAPI.releaseProfile(props.connection.id, signal);
    } catch (e) {
      if (e instanceof HttpError && e.status === 404) tool = defaultProfile();
      else throw e;
    }
    if (signal.aborted || n !== generation) return;
    grants.value = rows;
    profile.value = tool;
    if (uncertain.value) readBack.value = true;
  } catch (e) {
    if (!signal.aborted && n === generation) loadError.value = kubeError(e);
  } finally {
    if (n === generation) loading.value = false;
  }
}
watch(
  () => props.connection.id,
  () => {
    gate.reset();
    open.value = false;
    editing.value = undefined;
    resetGrant();
    void load();
  },
  { immediate: true },
);
onBeforeUnmount(() => request.abort());
function edit(row?: KubeReleaseGrant) {
  editing.value = row;
  resetGrant(row);
  open.value = true;
}
function resource() {
  grant.resources.push({
    resource: {
      group: "",
      version: "v1",
      resource: "configmaps",
      kind: "ConfigMap",
      namespace: "default",
      name: "",
    },
    actions: ["get"],
    containers: [],
    fields: [],
  });
}
function toggle(scope: KubeReleaseAccess, action: string) {
  scope.actions = scope.actions.includes(action)
    ? scope.actions.filter((a) => a !== action)
    : [...scope.actions, action];
}
function saveProfile() {
  if (!profile.value) return;
  const key = props.connection.id,
    input = JSON.parse(JSON.stringify(profile.value));
  gate.execute(async (signal) => {
    const saved = await kubeAPI.saveReleaseProfile(key, input, signal);
    if (!signal.aborted && props.connection.id === key) profile.value = saved;
  });
}
function saveGrant() {
  const key = props.connection.id,
    input = grantInput(grant),
    row = editing.value;
  gate.execute(async (signal) => {
    await kubeAPI.saveReleaseGrant(
      key,
      input,
      row?.id,
      row?.revision ?? 0,
      signal,
    );
    if (!signal.aborted && props.connection.id === key) {
      open.value = false;
      await load();
    }
  });
}
async function remove(row: KubeReleaseGrant) {
  if (
    !(await confirm.open({
      title: t("kubernetes.delete"),
      body: t("kubernetes.release.deleteGrantHint"),
      confirmLabel: t("kubernetes.delete"),
      variant: "danger",
    }))
  )
    return;
  const key = props.connection.id;
  gate.execute(async (signal) => {
    await kubeAPI.deleteReleaseGrant(key, row, signal);
    if (!signal.aborted && props.connection.id === key) await load();
  });
}
const executor = computed({
  get: () => JSON.stringify(grant.executor),
  set: (value: string) => {
    grant.executor = JSON.parse(value);
  },
});
</script>
<template>
  <section class="kube-surface kube-form">
    <header class="kube-head">
      <strong>{{ t("kubernetes.release.settings") }}</strong
      ><AppButton :loading="loading" @click="load">{{
        t("common.refresh")
      }}</AppButton>
    </header>
    <p class="kube-muted">{{ t("kubernetes.release.permissionHint") }}</p>
    <p v-if="loadError" role="alert" class="kube-error">{{ t(loadError) }}</p>
    <p v-if="error" role="alert" class="kube-error">{{ t(error) }}</p>
    <details v-if="profile">
      <summary>{{ t("kubernetes.profile") }} · v2</summary>
      <form class="kube-form" @submit.prevent="saveProfile">
        <fieldset class="kube-form" :disabled="busy || uncertain">
          <FormField
            :label="t('kubernetes.release.toolImage')"
            field-id="kr-tool-image"
            ><input
              id="kr-tool-image"
              v-model="profile.profile.runtime.image"
              required
          /></FormField>
          <div class="kube-grid">
            <FormField
              :label="t('kubernetes.release.architecture')"
              field-id="kr-tool-arch"
              ><select
                id="kr-tool-arch"
                v-model="profile.profile.runtime.architecture"
              >
                <option>amd64</option>
                <option>arm64</option>
              </select></FormField
            ><FormField
              :label="t('kubernetes.release.toolUID')"
              field-id="kr-tool-uid"
              ><input
                id="kr-tool-uid"
                v-model.number="profile.profile.runtime.uid"
                type="number"
                min="1"
                required
            /></FormField>
          </div>
          <FormField
            :label="t('kubernetes.release.kubectlPath')"
            field-id="kr-tool-kubectl"
            ><input
              id="kr-tool-kubectl"
              v-model="profile.profile.runtime.kubectlPath"
              required /></FormField
          ><FormField
            :label="t('kubernetes.release.helperPath')"
            field-id="kr-tool-helper"
            ><input
              id="kr-tool-helper"
              v-model="profile.profile.helperPath"
              required /></FormField
          ><FormField
            :label="t('kubernetes.release.helmPath')"
            field-id="kr-tool-helm"
            ><input
              id="kr-tool-helm"
              v-model="profile.profile.helmPath"
              required /></FormField
          ><FormField
            :label="t('kubernetes.release.registryCredential')"
            field-id="kr-tool-registry"
            ><input
              id="kr-tool-registry"
              v-model="
                profile.profile.runtime.registryCredentialId
              " /></FormField
          ><label class="kube-check"
            ><input v-model="profile.enabled" type="checkbox" />{{
              t("kubernetes.enabled")
            }}</label
          ><AppButton type="submit" :loading="busy">{{
            t("kubernetes.save")
          }}</AppButton>
        </fieldset>
      </form>
    </details>
    <header class="kube-head">
      <strong>{{ t("kubernetes.release.resourcePermissions") }}</strong
      ><AppButton :disabled="busy || uncertain" @click="edit()">{{
        t("kubernetes.addGrant")
      }}</AppButton>
    </header>
    <div v-for="row in grants" :key="row.id" class="kube-head">
      <span
        >{{
          projects.find((p) => p.id === row.projectId)?.name ?? row.projectId
        }}
        · {{ row.environment }} · {{ row.resources.length }}</span
      >
      <div class="kube-actions">
        <AppButton :disabled="busy || uncertain" @click="edit(row)">{{
          t("kubernetes.edit")
        }}</AppButton
        ><AppButton :disabled="busy || uncertain" @click="remove(row)">{{
          t("kubernetes.delete")
        }}</AppButton>
      </div>
    </div>
    <form v-if="open" class="kube-form" @submit.prevent="saveGrant">
      <fieldset class="kube-form" :disabled="busy || uncertain">
        <div class="kube-grid">
          <FormField
            :label="t('kubernetes.project')"
            field-id="kr-grant-project"
            ><select id="kr-grant-project" v-model="grant.projectId" required>
              <option value="">{{ t("kubernetes.select") }}</option>
              <option v-for="p in projects" :key="p.id" :value="p.id">
                {{ p.name }}
              </option>
            </select></FormField
          ><FormField
            :label="t('kubernetes.environment')"
            field-id="kr-grant-env"
            ><input
              id="kr-grant-env"
              v-model="grant.environment"
              required /></FormField
          ><FormField
            :label="t('kubernetes.executor')"
            field-id="kr-grant-executor"
            ><select id="kr-grant-executor" v-model="executor">
              <option
                v-for="e in connection.executors"
                :key="JSON.stringify(e)"
                :value="
                  JSON.stringify(e.kind === 'local' ? { kind: 'local' } : e)
                "
              >
                {{
                  e.kind === "local"
                    ? t("kubernetes.local")
                    : "SSH · " + e.serverId
                }}
              </option>
            </select></FormField
          >
        </div>
        <div
          v-for="(scope, index) in grant.resources"
          :key="index"
          class="kube-surface kube-form"
        >
          <div class="kube-grid">
            <FormField
              v-for="key in [
                'group',
                'version',
                'resource',
                'kind',
                'namespace',
                'name',
              ] as const"
              :key="key"
              :label="t('kubernetes.release.resourceFields.' + key)"
              :field-id="'kr-scope-' + index + '-' + key"
              ><input
                :id="'kr-scope-' + index + '-' + key"
                v-model="scope.resource[key]"
                :required="!['group', 'namespace'].includes(key)"
            /></FormField>
          </div>
          <div class="kube-action-options">
            <label v-for="action in actions" :key="action" class="kube-check"
              ><input
                type="checkbox"
                :checked="scope.actions.includes(action)"
                @change="toggle(scope, action)"
              />{{ t("kubernetes.release.actions." + action) }}</label
            >
          </div>
          <FormField
            :label="t('kubernetes.containers')"
            :field-id="'kr-scope-' + index + '-containers'"
            ><input
              :id="'kr-scope-' + index + '-containers'"
              :value="scope.containers?.join(', ')"
              @change="
                scope.containers = ($event.target as HTMLInputElement).value
                  .split(',')
                  .map((v) => v.trim())
                  .filter(Boolean)
              " /></FormField
          ><FormField
            :label="t('kubernetes.release.fieldPaths')"
            :field-id="'kr-scope-' + index + '-fields'"
          >
            <textarea
              :id="'kr-scope-' + index + '-fields'"
              :value="scope.fields?.join('\n')"
              @change="
                scope.fields = ($event.target as HTMLTextAreaElement).value
                  .split('\n')
                  .map((v) => v.trim())
                  .filter(Boolean)
              "
            /></FormField
          ><AppButton @click="grant.resources.splice(index, 1)">{{
            t("kubernetes.remove")
          }}</AppButton>
        </div>
        <AppButton @click="resource">{{
          t("kubernetes.release.addResource")
        }}</AppButton
        ><label class="kube-check"
          ><input v-model="grant.requireApproval" type="checkbox" />{{
            t("kubernetes.approvalRequired")
          }}</label
        ><label class="kube-check"
          ><input v-model="grant.enabled" type="checkbox" />{{
            t("kubernetes.enabled")
          }}</label
        >
        <div class="kube-actions">
          <AppButton @click="open = false">{{
            t("kubernetes.cancel")
          }}</AppButton
          ><AppButton
            type="submit"
            variant="primary"
            :loading="busy"
            :disabled="!grant.resources.length"
            >{{ t("kubernetes.save") }}</AppButton
          >
        </div>
      </fieldset>
    </form>
    <KubeReadBack
      v-if="uncertain"
      :loaded="readBack"
      :loading="loading"
      :records="grants"
      @read="load"
      @done="
        gate.reset();
        readBack = false;
        open = false;
      "
    /><KubePassword :state="gate" />
  </section>
</template>
