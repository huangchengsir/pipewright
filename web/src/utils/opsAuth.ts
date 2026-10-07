// A monotonic epoch invalidates pending responses as well as live subscriptions.
let epoch = 0
const listeners = new Set<() => void>()
export const opsAuthEpoch = (): number => epoch
export function resetOpsAuth(): void {
  epoch++
  for (const listener of listeners) listener()
}
export function onOpsAuthReset(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}
