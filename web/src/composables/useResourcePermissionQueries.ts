import { onScopeDispose, toValue, watch, type MaybeRefOrGetter } from "vue";
import type { PermissionQuery } from "../api/access";
import { usePermissionStore } from "../stores/permissions";
import { useSessionStore } from "../stores/session";
// A rendered resource list retains individual hints; the shared store owns the
// bounded cache, queued capacity, TTL and revocation checks.
export function useResourcePermissionQueries(
  queries: MaybeRefOrGetter<readonly PermissionQuery[]>,
) {
  const permissions = usePermissionStore(),
    session = useSessionStore();
  const stop = watch(
    () =>
      JSON.stringify([
        session.user?.multiTenant,
        permissions.activeTeam,
        toValue(queries),
      ]),
    (_v, _o, onCleanup) => {
      let current = true;
      let releases: (() => void)[] = [];
      queueMicrotask(() => {
        if (current && session.user?.multiTenant === true)
          releases = toValue(queries).map((q) =>
            permissions.retain(permissions.activeTeam, q),
          );
      });
      onCleanup(() => {
        current = false;
        const previous = releases;
        releases = [];
        queueMicrotask(() => previous.forEach((release) => release()));
      });
    },
    { immediate: true },
  );
  onScopeDispose(stop);
}
