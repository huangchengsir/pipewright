import { reactive, shallowReactive } from 'vue'
import type {
  Analysis,
  Call,
  Entry,
  Retry,
  Session,
  Snapshot,
  Turn,
} from '../api/opsChat'
import { onOpsAuthReset } from '../utils/opsAuth'

// Deliberately memory-only: the local instance database owns every body.
export interface PendingSubmission {
  kind: 'turn' | 'retry' | 'analysis'
  input: Turn | Retry | Analysis
  sentText?: string
  sentEditGeneration?: number
}
export interface LocalDraft {
  text: string
  baseline: string
  editGeneration?: number
  status: 'saved' | 'dirty' | 'saving' | 'failed' | 'conflict'
}
const state = shallowReactive({
  optimistic: [] as Entry[],
  sessions: [] as Session[],
  snapshot: null as Snapshot | null,
  entries: [] as Entry[],
  calls: {} as Record<string, Call>,
  currentId: '',
  pending: reactive({}) as Record<string, PendingSubmission>,
  drafts: reactive({}) as Record<string, LocalDraft>,
})
function clear(): void {
  state.optimistic = []
  state.sessions = []
  state.snapshot = null
  state.entries = []
  state.calls = {}
  state.currentId = ''
  state.pending = reactive({})
  state.drafts = reactive({})
}
onOpsAuthReset(clear)
export function useOpsChatStore() {
  return { state, clear }
}
