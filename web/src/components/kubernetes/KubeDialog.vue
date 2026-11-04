<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch, useId } from "vue";
import { useI18n } from "vue-i18n";
import AppButton from "../ui/AppButton.vue";
import "../../styles/kubernetes.css";
const props = defineProps<{ open: boolean; title: string; busy?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const { t } = useI18n();
const dialog = ref<HTMLDialogElement>();
const headingId = useId();
let previous: HTMLElement | null = null;
watch(
  () => props.open,
  async (open) => {
    if (open) {
      previous = document.activeElement as HTMLElement;
      await nextTick();
      if (props.open) {
        dialog.value?.showModal();
        (
          dialog.value?.querySelector<HTMLElement>("input,select,textarea") ??
          dialog.value?.querySelector<HTMLElement>("button")
        )?.focus();
      }
    } else {
      dialog.value?.close();
      if (previous?.isConnected) previous.focus();
    }
  },
  { immediate: true },
);
function cancel(e: Event) {
  e.preventDefault();
  if (!props.busy) emit("close");
}
onBeforeUnmount(() => {
  dialog.value?.close();
  if (previous?.isConnected) previous.focus();
});
</script>
<template>
  <dialog
    ref="dialog"
    :aria-labelledby="headingId"
    class="kube-dialog kube-surface"
    @cancel="cancel"
    @keydown.stop
  >
    <header class="kube-head">
      <h3 :id="headingId">{{ title }}</h3>
      <AppButton :disabled="busy" @click="emit('close')">{{
        t("kubernetes.close")
      }}</AppButton>
    </header>
    <slot />
  </dialog>
</template>
