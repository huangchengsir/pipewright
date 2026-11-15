import type {PermissionQuery} from '../api/access'
import type {CreateMCPToken} from '../api/mcp'
// Mirrors issuance checks; it does not authorize execution of a future run plan.
export function mcpIssueQueries(input:CreateMCPToken):PermissionQuery[] {
 const queries:PermissionQuery[]=input.projectIds.map(resourceId=>({action:'mcp.issue',resourceId}))
 if(input.canTrigger)for(const resourceId of input.projectIds)queries.push({action:'run.start',resourceId,create:true})
 for(const grant of input.configGrants??[]) {
  if(!grant.read)continue
  for(const section of grant.sections) {
   const resourceId=grant.projectId,domain=section==='project'?'project':'pipeline'
   queries.push({action:`${domain}.read`,resourceId})
   if(grant.propose||grant.apply)queries.push({action:`${domain}.edit`,resourceId})
  }
 }
 return [...new Map(queries.map(q=>[JSON.stringify(q),q])).values()]
}
