<script setup lang="ts">
import { usePermissions } from '../composables/usePermission'
import type { PermissionQuery } from '../api/access'
const props = withDefaults(defineProps<{ query: PermissionQuery | readonly PermissionQuery[]; team?: string; mode?: 'hide' | 'disabled' }>(), { mode: 'hide' })
const { allowed, pending, disabled } = usePermissions(() => Array.isArray(props.query) ? props.query : [props.query as PermissionQuery], () => props.team)
// A denied slot must bind its provided disabled value to the actual control.
// There is no wrapper div pretending to disable its child button or link.
</script>
<template>
 <slot v-if="allowed" :allowed="true" :disabled="false" :pending="false" />
 <slot v-else-if="mode === 'disabled'" name="denied" :allowed="false" :disabled="disabled" :pending="pending" />
</template>
