import { getActivePinia } from "pinia";
import { useSessionStore } from "../stores/session";
import { usePermissionStore } from "../stores/permissions";
import {
  getCurrentScope,
  computed,
  onBeforeUnmount,
  onMounted,
  readonly,
  shallowReadonly,
  shallowReactive,
  shallowRef,
  watch,
} from "vue";
import { getOnboardingStatus, type Snapshot } from "../api/onboarding";
import {
  ACTIVE_RUN_STATUSES,
  deriveOnboarding,
  qualifyingSuccess,
} from "./onboardingState";

const KEYS = {
  dismissed: "onboarding_dismissed",
  active: "onboarding_active",
  completed: "onboarding_completed",
  projectId: "onboarding_project_id",
} as const;
const preferences = shallowReactive({
  dismissed: false,
  active: false,
  completed: false,
  projectId: "",
  version: 0,
});
const dirtyPreferences = new Set<keyof typeof KEYS>();
let preferenceScope = "";
function identityContext() {
  if (!getActivePinia())
    return {
      key: "unavailable",
      storage: "",
      valid: false,
      team: "",
      multi: false,
    };
  const session = useSessionStore(),
    permissions = usePermissionStore(),
    user = session.user;
  const valid =
    !!user &&
    !user.mustChangePassword &&
    !session.isNetworkError &&
    (user.multiTenant === false ||
      (user.multiTenant === true && !!user.userId && !!permissions.activeTeam));
  const multi = user?.multiTenant === true;
  const key = JSON.stringify([
    user,
    session.isNetworkError,
    permissions.activeTeam,
  ]);
  const storage = valid
    ? multi
      ? `onboarding_v2:${encodeURIComponent(JSON.stringify([user!.userId, permissions.activeTeam]))}:`
      : "legacy"
    : "";
  return { key, storage, valid, team: permissions.activeTeam, multi };
}
function synchronizeIdentity() {
  const context = identityContext();
  if (preferenceScope !== context.key) {
    preferenceScope = context.key;
    dirtyPreferences.clear();
    Object.assign(preferences, {
      dismissed: false,
      active: false,
      completed: false,
      projectId: "",
    });
    preferences.version++;
  }
  return context;
}
function storageKey(key: keyof typeof KEYS, namespace: string) {
  return namespace === "legacy" ? KEYS[key] : namespace + key;
}
function fixedOptions(context: ReturnType<typeof identityContext>) {
  return {
    headers: context.multi ? { "X-Pipewright-Team": context.team } : undefined,
    throwOnUnauthorized: true,
  };
}

export function rereadOnboardingPreferences(): void {
  const context = synchronizeIdentity();
  if (!context.valid) return;
  try {
    if (!dirtyPreferences.has("dismissed"))
      preferences.dismissed =
        localStorage.getItem(storageKey("dismissed", context.storage)) === "1";
    if (!dirtyPreferences.has("active"))
      preferences.active =
        localStorage.getItem(storageKey("active", context.storage)) === "1";
    if (!dirtyPreferences.has("completed"))
      preferences.completed =
        localStorage.getItem(storageKey("completed", context.storage)) === "1";
    if (!dirtyPreferences.has("projectId"))
      preferences.projectId =
        localStorage.getItem(storageKey("projectId", context.storage)) ?? "";
  } catch {
    /* Preferences remain usable in memory when storage is unavailable. */
  }
}
rereadOnboardingPreferences();

function updatePreferences(
  values: Partial<
    Pick<typeof preferences, "dismissed" | "active" | "completed" | "projectId">
  >,
): void {
  const context = synchronizeIdentity();
  if (!context.valid) return;
  Object.assign(preferences, values);
  for (const [key, value] of Object.entries(values)) {
    try {
      const targetKey = storageKey(key as keyof typeof KEYS, context.storage);
      if (value === false || value === "") localStorage.removeItem(targetKey);
      else
        localStorage.setItem(targetKey, value === true ? "1" : String(value));
      dirtyPreferences.delete(key as keyof typeof KEYS);
    } catch {
      dirtyPreferences.add(key as keyof typeof KEYS);
    }
  }
  preferences.version++;
}
export function useOnboardingPreferences() {
  rereadOnboardingPreferences();
  if (getCurrentScope())
    watch(() => identityContext().key, rereadOnboardingPreferences, {
      flush: "sync",
    });
  return {
    preferences: readonly(preferences),
    continuing: computed(
      () =>
        preferences.active && !preferences.dismissed && !preferences.completed,
    ),
  };
}
export function isOnboardingDismissed(): boolean {
  rereadOnboardingPreferences();
  return preferences.dismissed;
}
export function activateOnboarding(): void {
  rereadOnboardingPreferences();
  if (!preferences.dismissed && !preferences.completed)
    updatePreferences({ active: true });
}
export function dismissOnboarding(): void {
  updatePreferences({ dismissed: true, active: false });
}
export function resetOnboarding(): void {
  updatePreferences({ dismissed: false, completed: false, active: true });
  rereadOnboardingPreferences();
}
export function selectOnboardingProject(projectId: string): void {
  updatePreferences({ projectId });
}
export function selectCreatedOnboardingProject(projectId: string): void {
  rereadOnboardingPreferences();
  if (preferences.active && !preferences.dismissed && !preferences.completed)
    selectOnboardingProject(projectId);
}
export function explicitLoginRedirect(raw: unknown): string | null {
  return typeof raw === "string" &&
    raw.startsWith("/") &&
    !/^\/[\\/]/.test(raw) &&
    !/[\r\n\t]/.test(raw)
    ? raw
    : null;
}
export function shouldAutoOnboard(snapshot: Snapshot): boolean {
  rereadOnboardingPreferences();
  return (
    !preferences.dismissed &&
    !preferences.completed &&
    snapshot.projectsState === "known" &&
    snapshot.runsState === "known" &&
    snapshot.successState === "known" &&
    snapshot.projectCount === 0 &&
    snapshot.runCount === 0 &&
    snapshot.projects.length === 0 &&
    snapshot.selectedProject === null &&
    !qualifyingSuccess(snapshot.success)
  );
}
export function canUseInstanceOnboarding(): boolean {
  if (!getActivePinia()) return false;
  const session = useSessionStore(), user = session.user;
  return !!user && !user.mustChangePassword && !session.isNetworkError &&
    (user.multiTenant === false || (user.multiTenant === true && user.instanceAdmin === true));
}

export async function onboardingLoginTarget(raw: unknown): Promise<string> {
  const explicit = explicitLoginRedirect(raw);
  if (explicit) return explicit;
  rereadOnboardingPreferences();
  if (preferences.dismissed || preferences.completed) return "/";
  const context = identityContext();
  if (!context.valid || !canUseInstanceOnboarding()) return "/";
  const controller = new AbortController();
  const stop = watch(
    () => identityContext().key,
    () => controller.abort(),
    { flush: "sync" },
  );
  try {
    const data = await getOnboardingStatus(
      "",
      controller.signal,
      fixedOptions(context),
    );
    if (controller.signal.aborted || context.key !== identityContext().key)
      return "/";
    return shouldAutoOnboard(data) ? "/onboarding" : "/";
  } catch {
    return "/";
  } finally {
    stop();
  }
}

export function useOnboardingStatus() {
  rereadOnboardingPreferences();
  const snapshot = shallowRef<Snapshot | null>(null);
  const loading = shallowRef(true);
  const error = shallowRef(false);
  let sequence = 0;
  let controller: AbortController | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let mounted = false;
  let accepting = false;

  function stop(): void {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    controller?.abort();
    sequence++;
  }
  function poll(): void {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    if (
      mounted &&
      !document.hidden &&
      preferences.active &&
      !preferences.dismissed &&
      !preferences.completed &&
      snapshot.value?.latestRun &&
      ACTIVE_RUN_STATUSES.some((s) => s === snapshot.value!.latestRun!.status)
    ) {
      timer = setTimeout(() => {
        void refresh();
      }, 5000);
    }
  }
  async function refresh(): Promise<void> {
    stop();
    const context = identityContext();
    if (!mounted || document.hidden || !context.valid || !canUseInstanceOnboarding()) {
      snapshot.value = null;
      loading.value = false;
      return;
    }
    const seq = sequence;
    const version = preferences.version;
    controller = new AbortController();
    const signal = controller.signal;
    loading.value = true;
    error.value = false;
    try {
      const data = await getOnboardingStatus(
        preferences.projectId,
        signal,
        fixedOptions(context),
      );
      if (
        !mounted ||
        signal.aborted ||
        seq !== sequence ||
        version !== preferences.version ||
        context.key !== identityContext().key
      )
        return;
      snapshot.value = data;
      accepting = true;
      if (
        data.selectedProject &&
        data.projects.some((p) => p.id === data.selectedProject!.id)
      )
        updatePreferences({ projectId: data.selectedProject.id });
      else if (data.projectsState === "known" && data.projectCount === 0)
        updatePreferences({ projectId: "" });
      if (data.successState === "known" && qualifyingSuccess(data.success))
        updatePreferences({ completed: true, active: false });
      accepting = false;
    } catch {
      if (
        !mounted ||
        signal.aborted ||
        seq !== sequence ||
        version !== preferences.version ||
        context.key !== identityContext().key
      )
        return;
      snapshot.value = null;
      error.value = true;
    } finally {
      if (mounted && seq === sequence) {
        loading.value = false;
        poll();
      }
    }
  }
  function select(projectId: string): void {
    if (!snapshot.value?.projects.some((p) => p.id === projectId)) return;
    snapshot.value = null;
    selectOnboardingProject(projectId);
    if (!preferences.active || preferences.dismissed || preferences.completed)
      void refresh();
  }
  function focus(): void {
    if (!document.hidden) void refresh();
  }
  function visibility(): void {
    if (document.hidden) {
      stop();
      loading.value = false;
    } else void refresh();
  }
  watch(
    () => preferences.version,
    () => {
      if (accepting || !mounted) return;
      stop();
      snapshot.value = null;
      loading.value = false;
      if (
        preferences.active &&
        !preferences.dismissed &&
        !preferences.completed
      )
        void refresh();
    },
    { flush: "sync" },
  );
  watch(
    () => identityContext().key,
    () => {
      stop();
      snapshot.value = null;
      loading.value = false;
      error.value = false;
      accepting = true;
      rereadOnboardingPreferences();
      if (mounted && identityContext().valid) activateOnboarding();
      accepting = false;
      if (mounted) void refresh();
    },
    { flush: "sync" },
  );
  onMounted(() => {
    activateOnboarding();
    mounted = true;
    window.addEventListener("focus", focus);
    document.addEventListener("visibilitychange", visibility);
    void refresh();
  });
  onBeforeUnmount(() => {
    mounted = false;
    stop();
    window.removeEventListener("focus", focus);
    document.removeEventListener("visibilitychange", visibility);
  });
  return {
    snapshot: shallowReadonly(snapshot),
    loading: readonly(loading),
    error: readonly(error),
    flow: computed(() =>
      deriveOnboarding(snapshot.value, loading.value, error.value),
    ),
    refresh,
    select,
  };
}
