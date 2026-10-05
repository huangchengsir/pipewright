import { computed, onBeforeUnmount, shallowRef, watch, type Ref } from 'vue'
import { getOnboardingStatus, type Snapshot } from '../api/onboarding'
import type { PipelineStage } from '../api/pipeline'
import { guidePhase, type GuideContext } from './pipelineGuideState'

export function usePipelineGuide(options: {
  projectId: Readonly<Ref<string>>; stages: Readonly<Ref<PipelineStage[]>>
  dirty: Readonly<Ref<boolean>>; context: Readonly<Ref<GuideContext>>
}) {
  const snapshot = shallowRef<Snapshot | null>(null)
  const loading = shallowRef(true)
  const error = shallowRef(false)
  const reviewedJobId = shallowRef<string | null>(null)
  let request: AbortController | undefined
  let sequence = 0
  let disposed = false

  async function refresh(): Promise<void> {
    request?.abort()
    const seq = ++sequence
    const id = options.projectId.value
    const controller = new AbortController()
    request = controller
    loading.value = true
    error.value = false
    try {
      const data = await getOnboardingStatus(id, controller.signal)
      if (disposed || controller.signal.aborted || seq !== sequence || id !== options.projectId.value) return
      snapshot.value = data
    } catch {
      if (disposed || controller.signal.aborted || seq !== sequence) return
      snapshot.value = null
      error.value = true
    } finally {
      if (!disposed && seq === sequence) loading.value = false
    }
  }
  watch(options.projectId, () => { snapshot.value = null; reviewedJobId.value = null; void refresh() }, { immediate: true })
  watch(() => options.context.value.selectedJobId, id => {
    if (id && id !== reviewedJobId.value) reviewedJobId.value = null
  })
  watch(options.stages, stages => {
    if (reviewedJobId.value && !stages.some(s => s.jobs.some(j => j.id === reviewedJobId.value))) reviewedJobId.value = null
  })
  onBeforeUnmount(() => { disposed = true; sequence++; request?.abort() })
  return {
    snapshot, loading, error, refresh,
    review: () => { reviewedJobId.value = options.context.value.selectedJobId },
    configure: () => { reviewedJobId.value = null },
    phase: computed(() => guidePhase({ stages: options.stages.value, projectId: options.projectId.value,
      dirty: options.dirty.value, context: options.context.value, reviewedJobId: reviewedJobId.value,
      snapshot: snapshot.value, loading: loading.value, error: error.value })),
  }
}
