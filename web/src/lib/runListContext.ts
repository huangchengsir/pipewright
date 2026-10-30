import type { LocationQueryRaw } from 'vue-router'
import { nextTick } from "vue";
const positions = new Map<string, { y: number; id: string }>();
export function saveRunListPosition(path: string, id = ""): void {
  positions.set(path, { y: window.scrollY, id });
  if (positions.size > 30) positions.delete(positions.keys().next().value!);
}
export async function restoreRunListPosition(
  path: string,
  isCurrent: () => boolean = () => true,
): Promise<void> {
  const saved = positions.get(path);
  if (!saved) return;
  await nextTick();
  requestAnimationFrame(() => {
    if (!isCurrent()) return;
    const row = Array.from(
      document.querySelectorAll<HTMLElement>("[data-run-id]"),
    ).find((el) => el.dataset.runId === saved.id);
    row?.focus({ preventScroll: true });
    window.scrollTo({ top: saved.y, behavior: "instant" });
  });
}
export function clearRunListPosition(path: string): void {
  positions.delete(path);
}
export function safeRunListReturn(value: unknown): string {
  if (
    typeof value !== "string" ||
    !/^\/runs(?:\?|$)/.test(value) ||
    value.length > 4096
  )
    return "/runs";
  // Never allow a different origin, hash or another route through a return parameter.
  try {
    const u = new URL(value, "https://pipewright.invalid");
    return u.origin === "https://pipewright.invalid" &&
      u.pathname === "/runs" &&
      !u.hash
      ? u.pathname + u.search
      : "/runs";
  } catch {
    return "/runs";
  }
}

/** Carry a validated list return path through a run-to-run link without
 * replacing its source step/stepId query. Existing callers without context
 * keep their original route shape. */
export function runDetailLocation(id: string, returnTo?: unknown, query: LocationQueryRaw = {}) {
  const target = { name: 'run-detail', params: { id } }
  const next = returnTo == null ? query : { ...query, returnTo: safeRunListReturn(returnTo) }
  return Object.keys(next).length ? { ...target, query: next } : target
}
