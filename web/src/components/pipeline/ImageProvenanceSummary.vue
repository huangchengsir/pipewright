<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ArrowUpRight } from '@vicons/tabler'
import type { ImageSource } from './imageProvenance'
import { projectImageName, registryRepository } from './imageProvenance'

const props = defineProps<{ source: ImageSource; projectName?: string }>()
const emit = defineEmits<{ inspect: [jobId: string] }>()
const { t } = useI18n()
const push = computed(() => props.source.job.type === 'push_image')
const config = computed(() => props.source.job.config)
const repository = computed(() => registryRepository(config.value.registry?.trim() ?? ''))
const imageName = computed(() => config.value.imageName?.trim() || (props.projectName ? projectImageName(props.projectName) : t('pipelineJob.imageNameFromProject')))
</script>

<template>
  <section class="image-provenance" :aria-label="t('pipelineJob.imageProvenanceTitle')">
    <div class="image-provenance__head">
      <h3>{{ t('pipelineJob.imageProvenanceTitle') }}</h3>
      <button type="button" :title="t('pipelineJob.inspectImageSource')" @click="emit('inspect', source.inspectJobId)">
        <ArrowUpRight aria-hidden="true" />{{ t('pipelineJob.inspectImageSource') }}
      </button>
    </div>
    <dl>
      <dt>{{ t('pipelineJob.imageSourceNode') }}</dt>
      <dd>{{ source.stage.name }} / {{ source.job.name }}</dd>
      <template v-if="push">
        <dt>{{ t('pipelineJob.fieldRegistryLabel') }}</dt>
        <dd :class="{ 'image-provenance__warning': config.registry && !repository }">{{ repository || t(config.registry ? 'pipelineJob.imageRegistryInvalid' : 'pipelineJob.imageRegistryFromEnvironment') }}</dd>
        <dt>{{ t('pipelineJob.fieldImageNameLabel') }}</dt><dd class="image-provenance__mono">{{ imageName }}</dd>
        <dt>{{ t('pipelineJob.imageVersionTemplate') }}</dt><dd class="image-provenance__mono">{{ config.tag?.trim() || '${COMMIT_SHA}' }}</dd>
      </template>
      <template v-else>
        <dt>{{ t('pipelineJob.fieldRegistryLabel') }}</dt><dd>{{ t('pipelineJob.imageBuildRegistry') }}</dd>
        <dt>{{ t('pipelineJob.imageVersionTemplate') }}</dt><dd>{{ t('pipelineJob.imageBuildVersion') }}</dd>
      </template>
    </dl>
    <p>{{ t('pipelineJob.imageRuntimeReference') }}</p>
  </section>
</template>

<style scoped>
.image-provenance { margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--color-border); }
.image-provenance__head { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; margin-bottom: 10px; }
h3 { margin: 0; font-size: 0.8rem; font-weight: 600; }
button { display: inline-flex; align-items: center; gap: 4px; padding: 3px 0; color: var(--color-primary); border: 0; background: transparent; font: inherit; font-size: 0.75rem; cursor: pointer; }
button svg { width: 14px; height: 14px; flex: none; }
button:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 3px; }
dl { display: grid; grid-template-columns: minmax(60px, 80px) minmax(0, 1fr); gap: 7px 10px; margin: 0; font-size: 0.77rem; line-height: 1.5; }
dt { color: var(--color-dim); } dd { margin: 0; overflow-wrap: anywhere; }
.image-provenance__mono { font-family: var(--font-mono); }
.image-provenance__warning { color: var(--color-amber); }
p { margin: 10px 0 0; color: var(--color-dim); font-size: 0.73rem; line-height: 1.6; }
</style>
