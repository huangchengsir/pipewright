<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DeployRunInput } from '../../api/runs'
import type {
  GuardDuration,
  GuardPlan,
  ReleaseGuardBinding,
} from '../../api/releaseGuard'
import AppButton from '../ui/AppButton.vue'
import ReleaseGuardPlan from './ReleaseGuardPlan.vue'

const props = withDefaults(
  defineProps<{
    projectId: string
    runId: string
    input: DeployRunInput
    disabled?: boolean
    resetKey?: number
  }>(),
  { disabled: false, resetKey: 0 },
)
const emit = defineEmits<{
  change: [
    state: { enabled: boolean; ready: boolean; binding?: ReleaseGuardBinding },
  ]
}>()
const { t } = useI18n()
const enabled = ref(false)
const open = ref(false)
const plan = ref<GuardPlan | null>(null)
const acceptedSignature = ref('')
const key = ref('')
const now = ref(Date.now())
const changed = ref(false)
const signature = computed(() =>
  JSON.stringify({
    projectId: props.projectId,
    runId: props.runId,
    ...props.input,
  }),
)
const attachedInput = computed(() => ({
  runId: props.runId,
  artifactId: props.input.artifactId,
  serverIds: [...props.input.serverIds],
  deployConfig: { ...(props.input.deployConfig ?? {}) },
  strategy: props.input.strategy,
  healthCheck: props.input.healthCheck,
}))
const validSelection = computed(
  () => !!props.input.artifactId && props.input.serverIds.length > 0,
)
const reviewedDuration = computed<GuardDuration>(() =>
  [300, 600, 900].includes(plan.value?.durationSeconds ?? 0)
    ? (plan.value!.durationSeconds as GuardDuration)
    : 600,
)
const ready = computed(
  () =>
    enabled.value &&
    !!plan.value &&
    acceptedSignature.value === signature.value &&
    Date.parse(plan.value.expiresAt) > now.value,
)
const clock = setInterval(() => {
  now.value = Date.now()
}, 1000)
function invalidate(): void {
  changed.value = !!plan.value
  plan.value = null
  acceptedSignature.value = ''
  key.value = ''
  open.value = false
}
function prepared(value: GuardPlan): void {
  plan.value = value
  acceptedSignature.value = signature.value
  key.value = crypto.randomUUID()
  changed.value = false
  now.value = Date.now()
  open.value = false
}
watch(signature, invalidate)
watch(() => props.resetKey, invalidate)
watch(enabled, (value) => {
  if (!value) invalidate()
})
watch(
  [enabled, ready, plan, key],
  () =>
    emit('change', {
      enabled: enabled.value,
      ready: ready.value,
      ...(ready.value && plan.value
        ? {
            binding: {
              planId: plan.value.id,
              planHash: plan.value.hash,
              idempotencyKey: key.value,
            },
          }
        : {}),
    }),
  { immediate: true },
)
onBeforeUnmount(() => clearInterval(clock))
</script>

<template>
  <section class="guard-attach">
    <label class="guard-attach__toggle"
      ><input v-model="enabled" type="checkbox" :disabled="disabled" /><span>{{
        t('releaseGuard.attached')
      }}</span></label
    >
    <template v-if="enabled"
      ><p>{{ t('releaseGuard.attachedDescription') }}</p>
      <p v-if="changed" class="guard-attach__warning" role="status">
        {{ t('releaseGuard.attachedChanged') }}
      </p>
      <p v-if="ready" class="guard-attach__ready">
        {{ t('releaseGuard.attachedReady') }}
      </p>
      <p v-else class="guard-attach__warning">
        {{ t('releaseGuard.attachedReviewRequired') }}
      </p>
      <AppButton :disabled="!validSelection || disabled" @click="open = true">{{
        t('releaseGuard.planTitle')
      }}</AppButton></template
    >
    <ReleaseGuardPlan
      v-if="open"
      :key="signature"
      :project-id="projectId"
      :attached-input="attachedInput"
      :initial-include-logs="plan?.includeLogs ?? false"
      :initial-duration-seconds="reviewedDuration"
      @close="open = false"
      @prepared="prepared"
    />
  </section>
</template>

<style scoped>
.guard-attach {
  padding: 14px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  display: grid;
  gap: 10px;
  background: var(--color-card-2);
}
.guard-attach__toggle {
  display: flex;
  gap: 10px;
  align-items: center;
  min-height: 44px;
  cursor: pointer;
  font-size: 0.875rem;
  font-weight: 600;
}
input {
  width: 18px;
  height: 18px;
  accent-color: var(--color-primary);
}
p {
  margin: 0;
  font-size: 0.8rem;
  line-height: 1.6;
  color: var(--color-dim);
}
.guard-attach__warning {
  color: var(--color-amber);
}
.guard-attach__ready {
  color: var(--color-green);
}
.guard-attach :deep(.app-btn) {
  min-height: 44px;
  justify-self: start;
}
@media (min-width: 640px) {
  :is(h3, h4) {
    font-size: 14px;
  }
  :is(p, label, select, li) {
    font-size: 13px;
  }
  :is(summary, label, select, a) {
    min-height: 34px;
  }
  .guard-attach :deep(.app-btn) {
    min-height: 34px;
    height: auto;
    padding-block: 6px;
    font-size: 13px;
    white-space: normal;
  }
}
@media (max-width: 639px) {
  :is(input:not([type='checkbox']):not([type='radio']), select, textarea) {
    font-size: 16px;
    min-height: 44px;
  }
}
</style>
