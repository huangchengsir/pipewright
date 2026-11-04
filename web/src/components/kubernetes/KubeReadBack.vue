<script setup lang="ts">
import { useI18n } from "vue-i18n";
import AppButton from "../ui/AppButton.vue";
defineProps<{
  loaded: boolean;
  loading?: boolean;
  records?: { id: string; name?: string; revision: number }[];
}>();
const emit = defineEmits<{ read: []; done: [] }>(),
  { t } = useI18n();
</script>
<template>
  <aside class="kube-card">
    <p class="kube-note">{{ t("kubernetes.errors.uncertain") }}</p>
    <AppButton :loading="loading" @click="emit('read')">{{
      t("kubernetes.readBack")
    }}</AppButton
    ><template v-if="loaded"
      ><p class="kube-muted">{{ t("kubernetes.readBackHint") }}</p>
      <ul v-if="records?.length">
        <li v-for="record in records" :key="record.id">
          <strong>{{ record.name ?? record.id }}</strong> ·
          {{ t("kubernetes.revision") }} {{ record.revision }}<br /><code>{{
            record.id
          }}</code>
        </li>
      </ul>
      <AppButton :disabled="loading" @click="emit('done')">{{
        t("kubernetes.acknowledge")
      }}</AppButton></template
    >
  </aside>
</template>
