<script setup lang="ts">
import { useId } from "vue";
import { useI18n } from "vue-i18n";
import type { useKubeMutation } from "../../composables/useKubeMutation";
import KubeDialog from "./KubeDialog.vue";
import AppButton from "../ui/AppButton.vue";
import FormField from "../ui/FormField.vue";
const props = defineProps<{ state: ReturnType<typeof useKubeMutation> }>();
const { passwordOpen, password, busy, error } = props.state;
const { t } = useI18n();
const passwordId = useId();
</script>
<template>
  <KubeDialog
    :open="passwordOpen"
    :title="t('kubernetes.passwordTitle')"
    :busy="busy"
    @close="state.cancel"
    ><form class="kube-form" @submit.prevent="state.verify">
      <p class="kube-muted">{{ t("kubernetes.passwordHint") }}</p>
      <FormField :field-id="passwordId" :label="t('kubernetes.password')"
        ><input
          :id="passwordId"
          v-model="password"
          type="password"
          autocomplete="current-password"
          required
          :disabled="busy"
      /></FormField>
      <p v-if="error" role="alert">{{ t(error) }}</p>
      <footer class="kube-actions">
        <AppButton :disabled="busy" @click="state.cancel">{{
          t("kubernetes.cancel")
        }}</AppButton
        ><AppButton type="submit" variant="primary" :loading="busy">{{
          t("kubernetes.confirm")
        }}</AppButton>
      </footer>
    </form></KubeDialog
  >
</template>
