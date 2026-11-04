import { onBeforeUnmount, ref } from "vue";
import { kubeAPI, kubeError, kubeUnknownWrite } from "../api/kubernetes";
type Action = (signal: AbortSignal) => Promise<void>;
/** A target change invalidates every pending continuation, including password verification. */
export function useKubeMutation() {
  const busy = ref(false),
    passwordOpen = ref(false),
    password = ref(""),
    error = ref(""),
    uncertain = ref(false);
  let until = 0,
    alive = true,
    pending: Action | null = null,
    controller = new AbortController(),
    generation = 0;
  function current(n: number, signal: AbortSignal) {
    return alive && generation === n && !signal.aborted;
  }
  async function perform(
    action: Action,
    n = generation,
    signal = controller.signal,
  ) {
    if (!current(n, signal)) return;
    busy.value = true;
    error.value = "";
    try {
      await action(signal);
    } catch (e) {
      if (current(n, signal)) {
        error.value = kubeError(e, true);
        uncertain.value = kubeUnknownWrite(e);
        if (error.value.endsWith("passwordRequired")) until = 0;
      }
    } finally {
      if (current(n, signal)) busy.value = false;
    }
  }
  function execute(action: Action) {
    if (busy.value || uncertain.value) return;
    if (Date.now() < until) {
      void perform(action);
      return;
    }
    pending = action;
    error.value = "";
    passwordOpen.value = true;
  }
  async function verify() {
    if (busy.value || !password.value || !pending) return;
    const n = generation,
      signal = controller.signal,
      action = pending;
    pending = null;
    busy.value = true;
    error.value = "";
    try {
      const result = await kubeAPI.reauth(password.value, signal);
      if (!current(n, signal)) return;
      until = Date.parse(result.reauthenticatedUntil);
      password.value = "";
      passwordOpen.value = false;
      await perform(action, n, signal);
    } catch (e) {
      if (current(n, signal)) {
        error.value = kubeError(e);
        pending = action;
      }
    } finally {
      if (current(n, signal)) {
        password.value = "";
        busy.value = false;
      }
    }
  }
  function invalidate() {
    generation++;
    controller.abort();
    controller = new AbortController();
    pending = null;
    password.value = "";
    passwordOpen.value = false;
    busy.value = false;
  }
  function cancel() {
    invalidate();
  }
  function reset() {
    invalidate();
    error.value = "";
    uncertain.value = false;
  }
  onBeforeUnmount(() => {
    alive = false;
    invalidate();
  });
  return {
    busy,
    passwordOpen,
    password,
    error,
    uncertain,
    execute,
    verify,
    cancel,
    reset,
  };
}
