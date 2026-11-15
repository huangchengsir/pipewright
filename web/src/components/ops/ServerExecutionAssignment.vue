<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getExecutionAssignment, setExecutionAssignment, type ExecutionAssignment } from '../../api/servers'
import { usePermission } from '../../composables/usePermission'
import { useSessionStore } from '../../stores/session'
import { usePermissionStore } from '../../stores/permissions'
import PermissionGate from '../PermissionGate.vue'
import AppButton from '../ui/AppButton.vue'
const props=defineProps<{serverId:string}>()
const {t}=useI18n();const session=useSessionStore();const permissions=usePermissionStore()
const query=computed(()=>({action:'server.assign',resourceId:props.serverId}))
const {allowed}=usePermission(query)
const visible=computed(()=>session.user?.multiTenant===true && session.user?.instanceAdmin===true && !session.isNetworkError && allowed.value)
const assignment=ref<ExecutionAssignment|null>(null),fingerprint=ref(''),busy=ref(false),loaded=ref(false),error=ref(false),saved=ref(false)
let generation=0
watch(()=>[props.serverId,visible.value,session.user?.userId,permissions.activeTeam],async (_new,_old,onCleanup)=>{
 const current=++generation;let active=true;onCleanup(()=>{active=false;generation++})
 assignment.value=null;fingerprint.value='';error.value=false;loaded.value=false;saved.value=false;busy.value=false
 if(!visible.value)return
 try {const result=await getExecutionAssignment(props.serverId);if(active&&current===generation&&visible.value){assignment.value=result;fingerprint.value=result?.hostKey??'';loaded.value=true}}
 catch {if(active&&current===generation){error.value=true}}
},{immediate:true})
async function save(revoke=false){
 if(!visible.value||!loaded.value||busy.value)return
 const current=generation,id=props.serverId
 busy.value=true;error.value=false;saved.value=false
 try {const result=await setExecutionAssignment(id,revoke?'':fingerprint.value.trim());if(current===generation&&visible.value){assignment.value=result;fingerprint.value=result?.hostKey??'';saved.value=true}}
 catch {if(current===generation){error.value=true}}
 finally {if(current===generation)busy.value=false}
}
</script>
<template>
 <section v-if="visible" class="execution-assignment" aria-labelledby="execution-assignment-title">
  <div class="execution-assignment-heading"><h4 id="execution-assignment-title">{{t('access.executor.title')}}</h4><span>{{loaded?t(assignment?'access.executor.assigned':'access.executor.unassigned'):t('access.loading')}}</span></div>
  <p>{{t('access.executor.hint')}}</p>
  <label for="execution-host-key">{{t('access.executor.fingerprint')}}</label>
  <input id="execution-host-key" v-model="fingerprint" type="text" class="execution-key" placeholder="SHA256:…" autocomplete="off" spellcheck="false" :disabled="!loaded||busy" aria-describedby="execution-key-help" />
  <p id="execution-key-help">{{t('access.executor.fingerprintHint')}}</p>
  <p v-if="error" role="alert">{{t('access.executor.error')}}</p><p v-if="saved" role="status">{{t('access.executor.saved')}}</p>
  <PermissionGate :query="query"><div class="execution-assignment-actions">
   <AppButton :disabled="busy||!loaded||!/^SHA256:[A-Za-z0-9+/]{43}$/.test(fingerprint.trim())" @click="save()">{{t('access.executor.approve')}}</AppButton>
   <AppButton v-if="assignment" variant="ghost" :disabled="busy" @click="save(true)">{{t('access.executor.revoke')}}</AppButton>
  </div></PermissionGate>
 </section>
</template>
<style scoped>
.execution-assignment{margin-top:1.25rem;padding-top:1rem;border-top:1px solid var(--color-border,#dfe3e8);font-size:.8125rem}.execution-assignment-heading{display:flex;align-items:center;justify-content:space-between;gap:.75rem}.execution-assignment h4{margin:0;font-size:.875rem}.execution-assignment p{line-height:1.55;color:var(--color-text-secondary,#687080);margin:.5rem 0}.execution-key{box-sizing:border-box;width:100%;margin-top:.375rem;padding:.5rem .625rem;border:1px solid var(--color-border,#dfe3e8);border-radius:.5rem;background:var(--color-bg-input,transparent);color:inherit;font-family:monospace;font-size:.8rem}.execution-assignment-actions{display:flex;gap:.5rem;flex-wrap:wrap;margin-top:.75rem}.execution-assignment [role=alert]{color:var(--color-danger,#b42318)}
</style>
