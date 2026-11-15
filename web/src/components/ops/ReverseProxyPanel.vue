<script setup lang="ts">
import {computed} from 'vue'
import type {ContainerInfo} from '../../api/containers'
import PermissionGate from '../PermissionGate.vue'
import ReverseProxyPanelContent from './ReverseProxyPanelContent.vue'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
const props=defineProps<{serverId:string;host:string;containers:ContainerInfo[]}>()
const emit=defineEmits<{(e:'count',n:number):void}>()
const session=useSessionStore(),permissions=usePermissionStore()
const identity=computed(()=>JSON.stringify([session.user,session.isNetworkError,permissions.activeTeam,props.serverId]))
</script>
<template><PermissionGate :query="{action:'proxy.read',resourceId:serverId}"><ReverseProxyPanelContent :key="identity" v-bind="props" @count="emit('count',$event)" /></PermissionGate></template>
