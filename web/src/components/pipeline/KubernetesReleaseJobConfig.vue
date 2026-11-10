<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import type { PipelineJob, PipelineStage } from "../../api/pipeline";
import {
  kubeAPI,
  kubeError,
  type KubeConnection,
  type KubeReleaseGrant,
} from "../../api/kubernetes";
import {
  releaseJSON,
  releaseField,
  selectReleaseMode,
} from "./kubernetesReleaseConfig";
import { isKubernetesStage } from "./kubernetesConfig";
import { dependsOn, hasAnyNeeds } from "./stageDeps";
import AppButton from "../ui/AppButton.vue";
import FormField from "../ui/FormField.vue";
import Source from "./KubeReleaseSource.vue";
import type { ReleaseSource } from "./kubernetesReleaseConfig";
import "../../styles/kubernetes.css";
const props = defineProps<{
  config: Record<string, string>;
  stages: PipelineStage[];
  stage: PipelineStage;
  job: PipelineJob;
  projectId?: string;
}>();
const emit = defineEmits<{ update: [Record<string, string>] }>();
const { t } = useI18n();
const connections = ref<KubeConnection[]>([]),
  grants = ref<KubeReleaseGrant[]>([]),
  loading = ref(false),
  error = ref(""),
  jsonError = ref("");
const checkDrafts = ref<Record<string,string>>({});
const checkDraftErrors = ref<Record<string,boolean>>({});
let emittedChecks: string | undefined;
function checkKey(check: Obj) {
 return JSON.stringify([props.projectId,props.job.id,check.namespace || props.config.namespace,check.kind,check.name]);
}
function refreshCheckError() {jsonError.value=Object.keys(checkDraftErrors.value).length ? t('kubernetes.release.invalidJSON') : '';}
function discardCheckDraft(check: Obj) {
 const key=checkKey(check);delete checkDrafts.value[key];delete checkDraftErrors.value[key];refreshCheckError();
}
function resetCheckDrafts() {checkDrafts.value={};checkDraftErrors.value={};refreshCheckError();}
watch(()=>[props.projectId,props.job.id],resetCheckDrafts);
watch(()=>props.config.checks,(value)=>{
 // Own accepted row operations retain other rows' private local drafts.
 // An external configuration replacement starts a new editing context.
 if(value!==emittedChecks) resetCheckDrafts();
 emittedChecks=undefined;
});
let request = new AbortController(),
  generation = 0;
type Obj = Record<string, any>;
const mode = computed(() => props.config.mode || "image"),
  workload = computed(() =>
    read<Obj>("workload", { kind: "Deployment", name: "" }),
  ),
  images = computed(() => read<Obj[]>("images", [])),
  source = computed(() =>
    read<ReleaseSource>("source", { kind: "inline", content: "" }),
  ),
  variables = computed(() => read<Obj[]>("variables", [])),
  checks = computed(() => read<Obj[]>("checks", [])),
  helm = computed(() => read<Obj>("helm", { release: "", hooks: "disabled" })),
  progressive = computed(() =>
    read<Obj>("progressive", {
      service: "",
      candidate: "",
      trackKey: "track",
      trackValue: "candidate",
      batches: [1],
    }),
  ),
  coreDNS = computed(() => read<Obj>("coreDNS", {})),
  eligible = computed(() =>
    grants.value.filter((g) => g.enabled && g.projectId === props.projectId),
  ),
  upstream = computed(() =>
    props.stages.filter((s, i) =>
      hasAnyNeeds(props.stages)
        ? dependsOn(props.stages, props.stage.id, s.id)
        : i < props.stages.findIndex((s) => s.id === props.stage.id),
    ),
  );
function enableCoreDNS(enabled: boolean) {
 if (!enabled) { const next={...props.config}; delete next.coreDNS; emit('update',next); return }
 set('coreDNS',{configMap:'coredns',key:'Corefile',deployment:'coredns',container:'coredns',service:'kube-dns',candidatePrefix:'pw-dns-canary',candidateServiceIP:'',dnsPort:53,serviceDomain:'kubernetes.default.svc.cluster.local',serviceResource:{group:'',version:'v1',resource:'services',kind:'Service',namespace:'default',name:'kubernetes'},records:[{name:'custom.example.invalid',expected:''}]})
}
function read<T>(key: string, fallback: T): T {
  return releaseJSON(props.config[key], fallback);
}
function set(key: string, value: unknown) {
  if(!Object.keys(checkDraftErrors.value).length) jsonError.value = "";
  const serialized=releaseField(value);
  if(key==='checks') emittedChecks=serialized;
  emit("update", { ...props.config, [key]: serialized });
}
function nested(key: string, field: string, value: unknown) {
  set(key, { ...read<Obj>(key, {}), [field]: value });
}
function rows(key: string, index: number, field: string, value: unknown) {
  if(key==='checks') discardCheckDraft(checks.value[index]);
  const next = read<Obj[]>(key, []).map((item, i) =>
    i === index ? { ...item, [field]: value } : item,
  );
  set(key, next);
}
function remove(key: string, index: number) {
  if(key==='checks') discardCheckDraft(checks.value[index]);
  set(
    key,
    read<Obj[]>(key, []).filter((_, i) => i !== index),
  );
}
function add(key: string, value: Obj) {
  set(key, [...read<Obj[]>(key, []), value]);
}
function json(key: string, value: string) {
  try {
    set(key, JSON.parse(value));
  } catch {
    jsonError.value = t("kubernetes.release.invalidJSON");
  }
}
function checkJSON(index: number, text: string) {
 const identity=checkKey(checks.value[index]);
 checkDrafts.value[identity]=text;
 try {
  const value:unknown=JSON.parse(text);
  const object=(v:unknown):v is Obj=>v!==null && typeof v==='object' && !Array.isArray(v);
  if(!object(value)) throw new Error();
  const strings=['kind','name','namespace','target','expected','server','protocol','positiveControl','predicate','serviceAccount'];
  const known=[...strings,'waitSeconds','restart','labels','imagePullSecrets','resource'];
  if(Object.keys(value).some(k=>!known.includes(k)) || strings.some(k=>k in value && typeof value[k]!=='string')) throw new Error();
  if('waitSeconds' in value && (!Number.isSafeInteger(value.waitSeconds))) throw new Error();
  if('restart' in value && typeof value.restart!=='boolean') throw new Error();
  if('labels' in value && (!object(value.labels)||Object.values(value.labels).some(v=>typeof v!=='string'))) throw new Error();
  if('imagePullSecrets' in value && (!Array.isArray(value.imagePullSecrets)||value.imagePullSecrets.some(v=>typeof v!=='string'))) throw new Error();
  if('resource' in value && value.resource!==null) {
   const keys=['group','version','resource','kind','namespace','name'];
   if(!object(value.resource)||Object.keys(value.resource).some(k=>!keys.includes(k))||Object.values(value.resource).some(v=>typeof v!=='string')) throw new Error();
  }
  delete checkDrafts.value[identity];delete checkDraftErrors.value[identity];refreshCheckError();
  set('checks',checks.value.map((c,i)=>i===index ? value : c));
 } catch {checkDraftErrors.value[identity]=true;refreshCheckError();}
}
function chooseConnection(key: string) {
  const c = connections.value.find((c) => c.id === key);
  emit("update", {
    ...props.config,
    connectionId: key,
    credentialId: c?.credentialId ?? "",
    context: c?.context ?? "",
  });
}
function chooseGrant(key: string) {
  const g = eligible.value.find((g) => g.id === key);
  if (!g) return;
  emit("update", {
    ...props.config,
    executor: JSON.stringify(
      g.executor.kind === "local"
        ? { kind: "local" }
        : { kind: "ssh", serverId: g.executor.serverId },
    ),
    namespace:
      g.resources.find((r) => r.resource.namespace)?.resource.namespace ||
      props.config.namespace,
  });
}
async function load() {
  request.abort();
  request = new AbortController();
  const signal = request.signal,
    n = ++generation;
  loading.value = true;
  error.value = "";
  try {
    const data = await kubeAPI.connections(signal);
    if (signal.aborted || n !== generation) return;
    connections.value = data;
    grants.value = props.config.connectionId
      ? await kubeAPI.releaseGrants(props.config.connectionId, signal)
      : [];
  } catch (e) {
    if (!signal.aborted && n === generation) error.value = kubeError(e);
  } finally {
    if (n === generation) loading.value = false;
  }
}
watch(() => [props.config.connectionId, props.projectId], load, {
  immediate: true,
});
onBeforeUnmount(() => request.abort());
function imageSource(index: number, kind: string) {
  rows(
    "images",
    index,
    "source",
    kind === "external"
      ? { kind, reference: "" }
      : { kind, artifact: { stageId: "", jobId: "", declarationIndex: 0 } },
  );
}
function imageField(index: number, key: string, value: unknown) {
  rows("images", index, "source", {
    ...images.value[index].source,
    [key]: value,
  });
}
function imageArtifact(index: number, key: string, value: string) {
  imageField(index, "artifact", {
    ...images.value[index].source.artifact,
    [key]: key === "declarationIndex" ? Number(value) : value,
  });
}
function fixed(index: number, value: string) {
  const variable = variables.value[index];
  if (variable.type === "string") {
    rows("variables", index, "value", value);
    return;
  }
  try {
    rows("variables", index, "value", JSON.parse(value));
  } catch {
    jsonError.value = t("kubernetes.release.invalidJSON");
  }
}
function variableSource(index: number, value: string) {
  const next: Obj = { ...variables.value[index], source: value };
  delete next.ref;
  delete next.value;
  if (value === "fixed") next.value = "";
  else next.ref = "";
  set(
    "variables",
    variables.value.map((v, i) => (i === index ? next : v)),
  );
}
const variableExample = "$" + "{NAME}",
  literalExample = "$" + "$" + "{NAME}";
</script>
<template>
  <section class="kube-surface kube-form kube-release-editor">
    <p v-if="!isKubernetesStage(stage)" role="alert" class="kube-error">
      {{ t("kubernetes.separateStage") }}
    </p>
    <p v-if="error" role="alert" class="kube-error">{{ t(error) }}</p>
    <p v-if="jsonError" role="alert" class="kube-error">{{ jsonError }}</p>
    <fieldset class="kube-form" :disabled="loading">
      <legend>{{ t("kubernetes.target") }}</legend>
      <FormField :label="t('kubernetes.connection')" field-id="kr-connection"
        ><select
          id="kr-connection"
          :value="config.connectionId"
          @change="chooseConnection(($event.target as HTMLSelectElement).value)"
        >
          <option value="">{{ t("kubernetes.select") }}</option>
          <option
            v-for="c in connections"
            :key="c.id"
            :value="c.id"
            :disabled="!c.enabled"
          >
            {{ c.name }} · {{ c.context }}
          </option>
        </select></FormField
      ><FormField :label="t('kubernetes.grants')" field-id="kr-grant"
        ><select
          id="kr-grant"
          @change="chooseGrant(($event.target as HTMLSelectElement).value)"
        >
          <option value="">{{ t("kubernetes.select") }}</option>
          <option v-for="g in eligible" :key="g.id" :value="g.id">
            {{ g.environment }} ·
            {{ g.executor.kind === "ssh" ? "SSH" : t("kubernetes.local") }} ·
            {{ g.resources.length }}
          </option>
        </select></FormField
      >
      <p
        v-if="config.connectionId && !eligible.length && !loading"
        class="kube-note"
      >
        {{ t("kubernetes.noProjectGrant") }}
      </p>
      <div class="kube-grid">
        <FormField :label="t('kubernetes.namespace')" field-id="kr-ns"
          ><input
            id="kr-ns"
            :value="config.namespace"
            @input="
              set('namespace', ($event.target as HTMLInputElement).value)
            " /></FormField
        ><FormField :label="t('kubernetes.executor')" field-id="kr-executor"
          ><select
            id="kr-executor"
            :value="config.executor"
            @change="
              set('executor', ($event.target as HTMLSelectElement).value)
            "
          >
            <option
              v-for="e in connections.find((c) => c.id === config.connectionId)
                ?.executors ?? []"
              :key="JSON.stringify(e)"
              :value="
                JSON.stringify(
                  e.kind === 'local'
                    ? { kind: 'local' }
                    : { kind: 'ssh', serverId: e.serverId },
                )
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
    </fieldset>
    <fieldset class="kube-form">
      <legend>{{ t("kubernetes.release.delivery") }}</legend>
      <FormField :label="t('kubernetes.release.mode')" field-id="kr-mode"
        ><select
          id="kr-mode"
          :value="mode"
          @change="
            emit(
              'update',
              selectReleaseMode(
                config,
                ($event.target as HTMLSelectElement).value,
              ),
            )
          "
        >
          <option
            v-for="m in ['image', 'manifest', 'helm', 'progressive']"
            :key="m"
            :value="m"
          >
            {{ t("kubernetes.release.modes." + m) }}
          </option>
        </select></FormField
      >
      <template v-if="mode === 'image' || mode === 'progressive'"
        ><div class="kube-grid">
          <FormField
            :label="t('kubernetes.release.workload')"
            field-id="kr-kind"
            ><select
              id="kr-kind"
              :value="workload.kind"
              @change="
                nested(
                  'workload',
                  'kind',
                  ($event.target as HTMLSelectElement).value,
                )
              "
            >
              <option
                v-for="kind in mode === 'progressive'
                  ? ['Deployment']
                  : ['Deployment', 'StatefulSet', 'DaemonSet']"
                :key="kind"
              >
                {{ kind }}
              </option>
            </select></FormField
          ><FormField :label="t('kubernetes.name')" field-id="kr-name"
            ><input
              id="kr-name"
              :value="workload.name"
              @input="
                nested(
                  'workload',
                  'name',
                  ($event.target as HTMLInputElement).value,
                )
              "
          /></FormField>
        </div>
        <div
          v-for="(image, i) in images"
          :key="i"
          class="kube-surface kube-form"
        >
          <FormField
            :label="t('kubernetes.container')"
            :field-id="'kr-image-' + i"
            ><input
              :id="'kr-image-' + i"
              :value="image.container"
              @input="
                rows(
                  'images',
                  i,
                  'container',
                  ($event.target as HTMLInputElement).value,
                )
              " /></FormField
          ><select
            :aria-label="t('kubernetes.imageSource')"
            :value="image.source.kind"
            @change="imageSource(i, ($event.target as HTMLSelectElement).value)"
          >
            <option value="external">
              {{ t("kubernetes.release.external") }}
            </option>
            <option value="artifact">
              {{ t("kubernetes.release.artifact") }}
            </option></select
          ><input
            v-if="image.source.kind === 'external'"
            :aria-label="t('kubernetes.imageSource')"
            :value="image.source.reference"
            placeholder="registry.example/app@sha256:…"
            @input="
              imageField(
                i,
                'reference',
                ($event.target as HTMLInputElement).value,
              )
            "
          /><input
            v-if="image.source.kind === 'external'"
            :aria-label="t('kubernetes.release.registryCredential')"
            :value="image.source.credentialId ?? ''"
            @input="
              imageField(
                i,
                'credentialId',
                ($event.target as HTMLInputElement).value,
              )
            "
          />
          <div v-else class="kube-grid">
            <select
              :aria-label="t('kubernetes.release.stage')"
              :value="image.source.artifact.stageId"
              @change="
                imageArtifact(
                  i,
                  'stageId',
                  ($event.target as HTMLSelectElement).value,
                )
              "
            >
              <option value="">{{ t("kubernetes.select") }}</option>
              <option v-for="s in upstream" :key="s.id" :value="s.id">
                {{ s.name }}
              </option></select
            ><select
              :aria-label="t('kubernetes.release.job')"
              :value="image.source.artifact.jobId"
              @change="
                imageArtifact(
                  i,
                  'jobId',
                  ($event.target as HTMLSelectElement).value,
                )
              "
            >
              <option value="">{{ t("kubernetes.select") }}</option>
              <option
                v-for="j in upstream
                  .find((s) => s.id === image.source.artifact.stageId)
                  ?.jobs.filter((j) => j.type === 'push_image') ?? []"
                :key="j.id"
                :value="j.id"
              >
                {{ j.name }}
              </option></select
            ><input
              type="number"
              min="0"
              :aria-label="t('kubernetes.release.declaration')"
              :value="image.source.artifact.declarationIndex"
              @change="
                imageArtifact(
                  i,
                  'declarationIndex',
                  ($event.target as HTMLInputElement).value,
                )
              "
            />
          </div>
          <AppButton @click="remove('images', i)">{{
            t("kubernetes.remove")
          }}</AppButton>
        </div>
        <AppButton
          @click="
            add('images', {
              container: '',
              source: { kind: 'external', reference: '' },
            })
          "
          >{{ t("kubernetes.release.addImage") }}</AppButton
        >
        <p class="kube-muted">
          {{ t("kubernetes.release.digestHint") }}
        </p></template
      >
      <Source
        v-if="mode === 'manifest' || mode === 'helm'"
        prefix="kr-files"
        :model-value="source"
        :stages="upstream"
        :chart="mode === 'helm'"
        @update:model-value="set('source', $event)"
      />
      <template v-if="mode === 'helm'"
        ><FormField
          :label="t('kubernetes.release.helmRelease')"
          field-id="kr-helm-name"
          ><input
            id="kr-helm-name"
            :value="helm.release"
            @input="
              nested(
                'helm',
                'release',
                ($event.target as HTMLInputElement).value,
              )
            " /></FormField
        ><label class="kube-check"
          ><input
            type="checkbox"
            :checked="helm.hooks === 'approved-jobs'"
            @change="
              nested(
                'helm',
                'hooks',
                ($event.target as HTMLInputElement).checked
                  ? 'approved-jobs'
                  : 'disabled',
              )
            "
          />{{ t("kubernetes.release.hooks") }}</label
        >
        <details>
          <summary>{{ t("kubernetes.release.values") }}</summary>
          <AppButton
            v-if="!helm.values"
            @click="nested('helm', 'values', { kind: 'inline', content: '{}' })"
            >{{ t("kubernetes.release.addValues") }}</AppButton
          ><Source
            v-else
            prefix="kr-values"
            :model-value="helm.values"
            :stages="upstream"
            @update:model-value="nested('helm', 'values', $event)"
          /></details
      ></template>
      <template v-if="mode === 'progressive'"
        ><div class="kube-grid">
          <FormField
            v-for="key in ['service', 'candidate', 'trackKey', 'trackValue']"
            :key="key"
            :label="t('kubernetes.release.' + key)"
            :field-id="'kr-p-' + key"
            ><input
              :id="'kr-p-' + key"
              :value="progressive[key]"
              @input="
                nested(
                  'progressive',
                  key,
                  ($event.target as HTMLInputElement).value,
                )
              "
          /></FormField>
        </div>
        <FormField
          :label="t('kubernetes.release.batches')"
          field-id="kr-batches"
          ><input
            id="kr-batches"
            :value="progressive.batches.join(', ')"
            @change="
              nested(
                'progressive',
                'batches',
                ($event.target as HTMLInputElement).value
                  .split(',')
                  .map(Number),
              )
            "
        /></FormField>
        <p class="kube-muted">
          {{ t("kubernetes.release.pauseHint") }}
        </p></template
      >
    </fieldset>
    <fieldset class="kube-form">
      <legend>{{ t("kubernetes.release.variables") }}</legend>
      <div
        v-for="(variable, i) in variables"
        :key="i"
        class="kube-surface kube-form"
      >
        <div class="kube-grid">
          <input
            :aria-label="t('kubernetes.name')"
            :value="variable.name"
            @input="
              rows(
                'variables',
                i,
                'name',
                ($event.target as HTMLInputElement).value,
              )
            "
          /><select
            :aria-label="t('kubernetes.release.type')"
            :value="variable.type"
            @change="
              rows(
                'variables',
                i,
                'type',
                ($event.target as HTMLSelectElement).value,
              )
            "
          >
            <option
              v-for="type in [
                'string',
                'number',
                'boolean',
                'object',
                'array',
                'null',
              ]"
              :key="type"
            >
              {{ type }}
            </option></select
          ><select
            :aria-label="t('kubernetes.release.source')"
            :value="variable.source"
            @change="
              variableSource(i, ($event.target as HTMLSelectElement).value)
            "
          >
            <option
              v-for="kind in [
                'fixed',
                'parameter',
                'environment',
                'output',
                'secret',
              ]"
              :key="kind"
              :value="kind"
            >
              {{ t("kubernetes.release.variableSources." + kind) }}
            </option>
          </select>
        </div>
        <input
          v-if="variable.source !== 'fixed'"
          :aria-label="t('kubernetes.release.reference')"
          :value="variable.ref"
          @input="
            rows(
              'variables',
              i,
              'ref',
              ($event.target as HTMLInputElement).value,
            )
          "
        /><textarea
          v-else
          :aria-label="t('kubernetes.release.value')"
          :value="
            variable.type === 'string'
              ? variable.value
              : JSON.stringify(variable.value)
          "
          @change="fixed(i, ($event.target as HTMLTextAreaElement).value)"
        /><AppButton @click="remove('variables', i)">{{
          t("kubernetes.remove")
        }}</AppButton>
      </div>
      <AppButton
        @click="
          add('variables', {
            name: '',
            type: 'string',
            source: 'parameter',
            ref: '',
          })
        "
        >{{ t("kubernetes.release.addVariable") }}</AppButton
      >
      <p class="kube-muted">
        {{
          t("kubernetes.release.variableHint", {
            variable: variableExample,
            literal: literalExample,
          })
        }}
      </p>
    </fieldset>
    <fieldset class="kube-form">
      <legend>{{ t("kubernetes.release.health") }}</legend>
      <p class="kube-muted">{{ t("kubernetes.release.healthHint") }}</p>
      <div class="kube-grid">
        <FormField
          v-for="key in ['timeoutSeconds', 'observeSeconds']"
          :key="key"
          :label="t('kubernetes.release.' + key)"
          :field-id="'kr-' + key"
          ><input
            :id="'kr-' + key"
            type="number"
            min="0"
            :value="config[key]"
            @change="set(key, ($event.target as HTMLInputElement).value)"
        /></FormField>
      </div>
      <div v-for="(check, i) in checks" :key="i" class="kube-surface kube-form">
        <select
          :aria-label="t('kubernetes.release.type')"
          :value="check.kind"
          @change="
            rows(
              'checks',
              i,
              'kind',
              ($event.target as HTMLSelectElement).value,
            )
          "
        >
          <option
            v-for="kind in [
              'workload',
              'condition',
              'http',
              'dns',
              'tcp',
              'network-allow',
              'network-deny',
              'storage',
            ]"
            :key="kind"
            :value="kind"
          >
            {{ t("kubernetes.release.checkKinds." + kind) }}
          </option>
        </select>
        <div class="kube-grid">
          <FormField
            v-for="key in [
              'name',
              'namespace',
              'target',
              'expected',
              'server',
              'protocol',
              'positiveControl',
              'predicate',
              'serviceAccount',
              'waitSeconds',
            ]"
            :key="key"
            :label="t('kubernetes.release.checkFields.' + key)"
            :field-id="'kr-check-' + i + '-' + key"
            ><input
              :id="'kr-check-' + i + '-' + key"
              :value="check[key] ?? ''"
              @change="
                rows(
                  'checks',
                  i,
                  key,
                  key === 'waitSeconds'
                    ? Number(($event.target as HTMLInputElement).value)
                    : ($event.target as HTMLInputElement).value,
                )
              "
          /></FormField>
        </div>
        <details>
          <summary>{{ t("kubernetes.release.advanced") }}</summary>
          <textarea
            class="kube-code"
            :aria-label="t('kubernetes.release.health')"
            :value="checkDrafts[checkKey(check)] ?? JSON.stringify(check, null, 2)"
            rows="8"
            @change="checkJSON(i, ($event.target as HTMLTextAreaElement).value)"
          />
        </details>
        <AppButton @click="remove('checks', i)">{{
          t("kubernetes.remove")
        }}</AppButton>
      </div>
      <AppButton
        @click="
          add('checks', {
            kind: 'workload',
            name: '',
            namespace: config.namespace,
          })
        "
        >{{ t("kubernetes.release.addCheck") }}</AppButton
      >
      <div v-if="mode === 'manifest'" class="kube-form">
        <label class="kube-check"><input type="checkbox" :checked="!!config.coreDNS" @change="enableCoreDNS(($event.target as HTMLInputElement).checked)" />{{ t('kubernetes.release.coreDNSEnable') }}</label>
        <template v-if="config.coreDNS">
          <p class="kube-muted">{{ t('kubernetes.release.coreDNSHint') }}</p>
          <div class="kube-grid">
            <FormField v-for="key in ['configMap','key','deployment','container','service','candidatePrefix','candidateServiceIP','dnsPort','serviceDomain']" :key="key" :label="t('kubernetes.release.coreDNSFields.' + key)" :field-id="'kr-dns-' + key">
              <input :id="'kr-dns-' + key" :type="key === 'dnsPort' ? 'number' : 'text'" :value="coreDNS[key] ?? ''" @change="nested('coreDNS',key,key === 'dnsPort' ? Number(($event.target as HTMLInputElement).value) : ($event.target as HTMLInputElement).value)" />
            </FormField>
          </div>
          <div v-for="(record,index) in (coreDNS.records ?? [])" :key="index" class="kube-grid">
            <FormField v-for="key in ['name','expected']" :key="key" :label="t('kubernetes.release.coreDNSFields.' + key)" :field-id="'kr-dns-record-' + index + '-' + key">
              <input :id="'kr-dns-record-' + index + '-' + key" :value="record[key] ?? ''" @change="nested('coreDNS','records',coreDNS.records.map((row: Obj,i: number)=>i===index ? {...row,[key]:($event.target as HTMLInputElement).value} : row))" />
            </FormField>
          </div>
          <details><summary>{{ t('kubernetes.release.advanced') }}</summary>
            <textarea class="kube-code" aria-label="CoreDNS" :value="JSON.stringify(coreDNS,null,2)" rows="12" @change="json('coreDNS',($event.target as HTMLTextAreaElement).value)" />
          </details>
        </template>
      </div>
    </fieldset>
  </section>
</template>
