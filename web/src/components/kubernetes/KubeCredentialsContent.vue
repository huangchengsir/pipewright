<script setup lang="ts">
import ResourcePager from "../ui/ResourcePager.vue";
import {usePagedResources} from "../../composables/usePagedResources";
import { useIdentityRequestScope } from "../../composables/useAuthorizedRequestScope";
import { usePermission } from "../../composables/usePermission";
import { useResourcePermissionQueries } from "../../composables/useResourcePermissionQueries";
import { usePermissionStore } from "../../stores/permissions";

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
  type KubeCredential,
  type KubeCredentialInput,
  type KubePreview,
} from "../../api/kubernetes";
import { useKubeMutation } from "../../composables/useKubeMutation";
import { useConfirm } from "../../composables/useConfirm";
import AppButton from "../ui/AppButton.vue";
import FormField from "../ui/FormField.vue";
import KubeDialog from "./KubeDialog.vue";
import KubeReadBack from "./KubeReadBack.vue";
import KubePassword from "./KubePassword.vue";
import "../../styles/kubernetes.css";
const { t } = useI18n(),
  confirm = useConfirm(),
  gate = useKubeMutation(() => scope.requestOptions);
const { busy, error, uncertain } = gate;
const readBack = ref(false);
watch(uncertain, () => {
  readBack.value = false;
});
function acknowledge() {
  open.value = false;
  clear();
  editing.value = undefined;
  gate.reset();
  readBack.value = false;
}
const rows = ref<KubeCredential[]>([]),
  loading = ref(false),
  loadError = ref(""),
  open = ref(false);
const editing = ref<KubeCredential>(),
  preview = ref<KubePreview>(),
  draft = reactive<KubeCredentialInput>(blank());
let controller = new AbortController(),
  sequence = 0,
  alive = true;
const permissions = usePermissionStore();
const scope = useIdentityRequestScope(() => {
  alive = false;
  sequence++;
  controller.abort();
  gate.reset();
  rows.value = [];
  open.value = false;
  editing.value = undefined;
  clear();
  loadError.value = "";
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
  action: "kubernetes_credential.create",
  resourceId: "",
  create: true,
});
const editQuery = computed(() =>
  editing.value
    ? { action: "kubernetes_credential.edit", resourceId: editing.value.id }
    : { action: "kubernetes_credential.create", resourceId: "", create: true },
);
const editPermission = usePermission(editQuery);
const rowPages=usePagedResources(rows);
useResourcePermissionQueries(() =>
  rowPages.items.value.flatMap((row) =>
    [
      "kubernetes_credential.read",
      "kubernetes_credential.edit",
      "kubernetes_credential.delete",
    ].map((action) => ({ action, resourceId: row.id })),
  ),
);
const visibleRows = computed(() =>
  rowPages.items.value.filter((row) => can("kubernetes_credential.read", row.id)),
);
watch(
  () => [editPermission.allowed.value, editPermission.pending.value],
  ([allowed]) => {
    if (!allowed && open.value && !editPermission.pending.value) {
      open.value = false;
      clear();
      gate.reset();
    }
  },
);
function blank(): KubeCredentialInput {
  return {
    name: "",
    mode: "server_token",
    context: "",
    server: "",
    ca: "",
    token: "",
    kubeconfig: "",
    tlsServerName: "",
    enabled: true,
  };
}
function clear() {
  Object.assign(draft, blank());
  preview.value = undefined;
}
watch(
  () => [
    draft.mode,
    draft.context,
    draft.server,
    draft.ca,
    draft.token,
    draft.kubeconfig,
    draft.tlsServerName,
  ],
  () => {
    preview.value = undefined;
  },
);
async function load() {
  if (!scope.active()) return;
  controller.abort();
  controller = new AbortController();
  const signal = controller.signal,
    seq = ++sequence;
  loading.value = true;
  loadError.value = "";
  try {
    const data = await scope.run(() =>
      kubeAPI.credentials(signal, scope.requestOptions),
    );
    if (alive && !signal.aborted && seq === sequence) {
      rows.value = data;
      if (uncertain.value) readBack.value = true;
    }
  } catch (e) {
    if (alive && !signal.aborted && seq === sequence)
      loadError.value = kubeError(e);
  } finally {
    if (alive && seq === sequence) loading.value = false;
  }
}
function edit(row?: KubeCredential) {
  if (
    row
      ? !can("kubernetes_credential.edit", row.id)
      : !createPermission.allowed.value
  )
    return;
  if (busy.value || uncertain.value) return;
  clear();
  editing.value = row;
  if (row)
    Object.assign(draft, {
      name: row.name,
      mode: row.mode,
      context: row.context,
      server: row.server,
      tlsServerName: row.tlsServerName ?? "",
      enabled: row.enabled,
    });
  open.value = true;
}
function close() {
  if (busy.value) return;
  gate.cancel();
  open.value = false;
  clear();
  editing.value = undefined;
}
function input(): KubeCredentialInput {
  return {
    name: draft.name.trim(),
    mode: draft.mode,
    context: draft.context.trim(),
    enabled: draft.enabled,
    ...(draft.mode === "kubeconfig"
      ? { kubeconfig: draft.kubeconfig }
      : {
          server: draft.server?.trim(),
          ca: draft.ca,
          token: draft.token,
          tlsServerName: draft.tlsServerName?.trim(),
        }),
  };
}
function inspect() {
  if (!open.value || !scope.active() || !editPermission.allowed.value) return;
  gate.execute(async (signal) => {
    if (!open.value || !scope.active() || !editPermission.allowed.value) return;
    const data = await scope.run(() =>
      kubeAPI.preview(
        {
          ...input(),
          ...(editing.value ? { credentialId: editing.value.id } : {}),
        },
        signal,
        scope.requestOptions,
      ),
    );
    if (!signal.aborted) preview.value = data;
  });
}
function save() {
  if (!open.value || !scope.active() || !editPermission.allowed.value) return;
  gate.execute(async (signal) => {
    if (!open.value || !scope.active() || !editPermission.allowed.value) return;
    await scope.run(() =>
      kubeAPI.saveCredential(
        editing.value?.id,
        editing.value?.revision ?? 0,
        input(),
        signal,
        scope.requestOptions,
      ),
    );
    if (signal.aborted) return;
    open.value = false;
    clear();
    editing.value = undefined;
    await load();
  });
}
function toggle(row: KubeCredential) {
  if (!can("kubernetes_credential.edit", row.id)) return;
  gate.execute(async (signal) => {
    if (!can("kubernetes_credential.edit", row.id)) return;
    await scope.run(() =>
      kubeAPI.enableCredential(row, !row.enabled, signal, scope.requestOptions),
    );
    if (!signal.aborted) await load();
  });
}
async function remove(row: KubeCredential) {
  if (!can("kubernetes_credential.delete", row.id)) return;
  if (
    !(await confirm.open({
      title: t("kubernetes.delete"),
      body: t("kubernetes.deleteBody", { name: row.name }),
      confirmLabel: t("kubernetes.delete"),
      variant: "danger",
    }))
  )
    return;
  if (!alive || !can("kubernetes_credential.delete", row.id)) return;
  gate.execute(async (signal) => {
    if (!can("kubernetes_credential.delete", row.id)) return;
    await scope.run(() =>
      kubeAPI.deleteCredential(row, signal, scope.requestOptions),
    );
    if (!signal.aborted) await load();
  });
}
onBeforeRouteLeave(async () => {
  if (busy.value) return false;
  if (open.value) {
    open.value = false;
    gate.cancel();
    await nextTick();
    const leave = await confirm.open({
      title: t("kubernetes.unsaved"),
      body: t("kubernetes.unsavedBody"),
      confirmLabel: t("kubernetes.discard"),
    });
    if (!leave) {
      open.value = true;
      return false;
    }
  }
  clear();
  gate.cancel();
  return true;
});
onMounted(load);
onBeforeUnmount(() => {
  alive = false;
  controller.abort();
  clear();
});
</script>
<template>
  <section class="kube-surface kube-card" aria-labelledby="kube-vault-title">
    <header class="kube-head">
      <div>
        <h3 id="kube-vault-title">{{ t("kubernetes.credentials") }}</h3>
        <p class="kube-muted">{{ t("kubernetes.credentialsHint") }}</p>
      </div>
      <AppButton
        :disabled="busy || uncertain"
        v-if="createPermission.allowed.value"
        @click="edit()"
        >{{ t("kubernetes.addCredential") }}</AppButton
      >
    </header>
    <p v-if="loadError" class="kube-error" role="alert">
      {{ t(loadError) }}
      <AppButton @click="load">{{ t("common.refresh") }}</AppButton>
    </p>
    <p
      v-if="error && !open && !gate.passwordOpen.value"
      class="kube-error"
      role="alert"
    >
      {{ t(error) }}
    </p>
    <p v-if="loading" role="status">{{ t("kubernetes.loading") }}</p>
    <p v-else-if="!visibleRows.length && !loadError" class="kube-muted">
      {{ t("kubernetes.emptyCredentials") }}
    </p>
    <ResourcePager :page="rowPages.page.value" :pages="rowPages.pages.value" @change="rowPages.select" />
    <article v-for="row in visibleRows" :key="row.id" class="kube-row">
      <div class="kube-row-main">
        <strong>{{ row.name }}</strong>
        <span class="kube-status">{{
          t(row.enabled ? "kubernetes.enabled" : "kubernetes.disabled")
        }}</span>
        <p class="kube-muted">{{ row.context }} · {{ row.server }}</p>
        <details class="kube-details">
          <summary>{{ t("kubernetes.identity") }}</summary>
          <dl class="kube-detail">
            <dt>{{ t("kubernetes.credential") }}</dt>
            <dd>{{ row.id }}</dd>
            <dt>CA SHA-256</dt>
            <dd>{{ row.caFingerprint }}</dd>
          </dl>
        </details>
      </div>
      <div class="kube-actions">
        <AppButton
          :disabled="busy || uncertain"
          v-if="can('kubernetes_credential.edit', row.id)"
          @click="edit(row)"
          >{{ t("kubernetes.rotate") }}</AppButton
        ><AppButton
          :disabled="busy || uncertain"
          v-if="can('kubernetes_credential.edit', row.id)"
          @click="toggle(row)"
          >{{
            t(row.enabled ? "kubernetes.disable" : "kubernetes.enable")
          }}</AppButton
        ><AppButton
          variant="ghost"
          :disabled="busy || uncertain"
          v-if="can('kubernetes_credential.delete', row.id)"
          @click="remove(row)"
          >{{ t("kubernetes.delete") }}</AppButton
        >
      </div>
    </article>
    <KubeReadBack
      v-if="uncertain && !open"
      :loaded="readBack"
      :loading="loading"
      :records="visibleRows"
      @read="load"
      @done="acknowledge"
    />
    <KubeDialog
      :open="open"
      :title="t(editing ? 'kubernetes.rotate' : 'kubernetes.addCredential')"
      :busy="busy"
      @close="close"
      ><form class="kube-form" @submit.prevent="save">
        <KubeReadBack
          v-if="uncertain"
          :loaded="readBack"
          :loading="loading"
          :records="visibleRows"
          @read="load"
          @done="acknowledge"
        />
        <p class="kube-muted">{{ t("kubernetes.secretHint") }}</p>
        <FormField :label="t('kubernetes.name')" field-id="kc-name"
          ><input
            id="kc-name"
            v-model="draft.name"
            required
            maxlength="120"
            :disabled="busy || !editPermission.allowed.value"
        /></FormField>
        <div class="kube-grid">
          <FormField :label="t('kubernetes.authentication')" field-id="kc-mode"
            ><select
              id="kc-mode"
              v-model="draft.mode"
              :disabled="busy || !editPermission.allowed.value"
            >
              <option value="server_token">
                {{ t("kubernetes.serverToken") }}
              </option>
              <option value="kubeconfig">Kubeconfig</option>
            </select></FormField
          ><FormField :label="t('kubernetes.context')" field-id="kc-context"
            ><input
              id="kc-context"
              v-model="draft.context"
              required
              :disabled="busy || !editPermission.allowed.value"
          /></FormField>
        </div>
        <template v-if="draft.mode === 'server_token'"
          ><FormField :label="t('kubernetes.server')" field-id="kc-server"
            ><input
              id="kc-server"
              v-model="draft.server"
              type="url"
              placeholder="https://cluster.example:6443"
              required
              :disabled="busy || !editPermission.allowed.value" /></FormField
          ><FormField :label="t('kubernetes.ca')" field-id="kc-ca">
            <textarea
              id="kc-ca"
              v-model="draft.ca"
              spellcheck="false"
              required
              :disabled="busy || !editPermission.allowed.value"
            /></FormField
          ><FormField :label="t('kubernetes.token')" field-id="kc-token"
            ><input
              id="kc-token"
              v-model="draft.token"
              type="password"
              autocomplete="new-password"
              required
              :disabled="busy || !editPermission.allowed.value" /></FormField
          ><FormField :label="t('kubernetes.tlsName')" field-id="kc-tls"
            ><input
              id="kc-tls"
              v-model="draft.tlsServerName"
              :disabled="busy || !editPermission.allowed.value" /></FormField
        ></template>
        <FormField v-else label="Kubeconfig" field-id="kc-config">
          <textarea
            id="kc-config"
            v-model="draft.kubeconfig"
            spellcheck="false"
            autocomplete="off"
            required
            :disabled="busy || !editPermission.allowed.value"
          />
        </FormField>
        <label class="kube-check"
          ><input
            v-model="draft.enabled"
            type="checkbox"
            :disabled="busy || !editPermission.allowed.value"
          />{{ t("kubernetes.enabled") }}</label
        >
        <p v-if="preview" class="kube-note">
          {{ t("kubernetes.previewReady") }} · {{ preview.context }} ·
          {{ preview.server }}
        </p>
        <p v-if="error" class="kube-error" role="alert">{{ t(error) }}</p>
        <footer class="kube-actions">
          <AppButton :disabled="busy" @click="close">{{
            t("kubernetes.cancel")
          }}</AppButton
          ><AppButton
            :disabled="busy || uncertain || !editPermission.allowed.value"
            @click="inspect"
            >{{ t("kubernetes.preview") }}</AppButton
          ><AppButton
            type="submit"
            variant="primary"
            :disabled="uncertain || !editPermission.allowed.value"
            :loading="busy"
            >{{ t("kubernetes.save") }}</AppButton
          >
        </footer>
      </form></KubeDialog
    ><KubePassword :state="gate" />
  </section>
</template>
