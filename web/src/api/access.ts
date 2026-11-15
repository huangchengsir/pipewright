import { http } from "./http";
import type { Session } from "./account";
export type ScopeKind = "none" | "team" | "selected" | "own";
export interface ResourceRef {
  kind: string;
  id: string;
}
export interface AccessScope {
  resourceRefs?: ResourceRef[];
  kind: ScopeKind;
  projectIds?: string[];
  resourceIds?: string[];
  environmentIds?: string[];
  environmentPolicy?: "" | "selected" | "test";
}
export interface Grant {
  action: string;
  scope: AccessScope;
}
export interface Role {
  canManage?: boolean;
  id: string;
  tenantId: string;
  nameKey: string;
  presetKey?: string;
  revision: number;
  pages: string[];
  grants: Grant[];
  requiresSelectedBinding: boolean;
}
export interface RoleInput {
  name: string;
  pages: string[];
  grants: Grant[];
  requiresSelectedBinding: boolean;
  expectedRevision: number;
}
export interface Team {
  id: string;
  name: string;
  revision: number;
}
export interface Binding {
  roleId: string;
  scope: AccessScope;
}
export interface Member {
  canManage?: boolean;
  id: string;
  teamId: string;
  userId: string;
  username: string;
  displayName: string;
  enabled: boolean;
  accountEnabled: boolean;
  revision: number;
  bindings: Binding[];
}
export interface MemberInput {
  userId: string;
  enabled: boolean;
  bindings: Binding[];
  expectedRevision: number;
}
export interface AccessUser {
  id: string;
  username: string;
  displayName: string;
  enabled: boolean;
}
export interface InitialCredential {
  user: AccessUser;
  temporaryPassword: string;
  expiresAt: number;
}
export interface Choice {
  kind?: string;
  id: string;
  name: string;
  projectId?: string;
  purpose?: string;
}
export interface Choices {
  resources?: Choice[];
  projects: Choice[];
  environments: Choice[];
}
export interface Action {
  id: string;
  page: string;
  resourceKind: string;
  projectScoped: boolean;
  environmentRequired: boolean;
  sensitive: boolean;
  readAction?: string;
}
export interface PermissionQuery {
 action: string;
 resourceId: string;
 environmentId?: string;
 create?: boolean;
}
export interface PermissionBatch {
 userId: string;
 teamId: string;
 modeEpoch: number;
 revision: number;
 catalogVersion: number;
 allowed: boolean[];
}
const root = "/api/access";
const teamPath = (team: string) => `${root}/teams/${encodeURIComponent(team)}`;
export const access = {
 navigation: (team:string) => http.get<{userId:string;teamId:string;modeEpoch:number;revision:number;catalogVersion:number;pages:string[]}>(`${teamPath(team)}/navigation`,{throwOnUnauthorized:true}),
 decisions: (team: string, queries: PermissionQuery[]) => http.post<PermissionBatch>(`${teamPath(team)}/decisions`, { queries }, { throwOnUnauthorized: true }),
  preview: (
    team: string,
    input: {
      memberId: string;
      action: string;
      resourceId: string;
      draftRoleId?: string;
      draft?: RoleInput;
    },
  ) =>
    http.post<{
      currentAllowed: boolean;
      draftAllowed?: boolean;
      revision: number;
    }>(`${teamPath(team)}/preview`, input),
  users: (after = "") =>
    http.get<{ users: AccessUser[]; nextCursor: string }>(
      `${root}/users?after=${encodeURIComponent(after)}`,
    ),
  sessions: async () =>
    (await http.get<{ sessions: Session[] }>(`${root}/self/sessions`)).sessions,
  revokeSession: (id: string) =>
    http.delete<void>(`${root}/self/sessions/${encodeURIComponent(id)}`),
  teams: () => http.get<Team[]>(`${root}/teams`,{throwOnUnauthorized:true}),
  createTeam: (name: string) => http.post<Team>(`${root}/teams`, { name }),
  catalog: () =>
    http.get<{ version: number; actions: Action[] }>(`${root}/catalog`),
  roles: (team: string) => http.get<Role[]>(`${teamPath(team)}/roles`),
  members: (team: string) => http.get<Member[]>(`${teamPath(team)}/members`),
  choices: (team: string) => http.get<Choices>(`${teamPath(team)}/choices`),
  capabilities: (team: string) =>
    http.get<Record<string, boolean>>(`${teamPath(team)}/capabilities`),
  saveRole: (team: string, id: string, input: RoleInput) =>
    id
      ? http.put<Role>(
          `${teamPath(team)}/roles/${encodeURIComponent(id)}`,
          input,
        )
      : http.post<Role>(`${teamPath(team)}/roles`, input),
  deleteRole: (team: string, role: Role) =>
    http.delete<void>(
      `${teamPath(team)}/roles/${encodeURIComponent(role.id)}`,
      { body: JSON.stringify({ expectedRevision: role.revision }) },
    ),
  saveMember: (team: string, id: string, input: MemberInput) =>
    id
      ? http.put<Member>(
          `${teamPath(team)}/members/${encodeURIComponent(id)}`,
          input,
        )
      : http.post<Member>(`${teamPath(team)}/members`, input),
  lookup: (team: string, username: string) =>
    http.post<AccessUser>(`${teamPath(team)}/lookup-account`, { username }),
  createUser: (
    username: string,
    displayName: string,
    team: string,
    bindings: Binding[],
  ) =>
    http.post<InitialCredential>(`${root}/users`, {
      username,
      displayName,
      assignments: bindings.map((b) => ({ teamId: team, ...b })),
    }),
  updateUser: (id: string, displayName: string) =>
    http.patch<void>(`${root}/users/${encodeURIComponent(id)}`, {
      displayName,
    }),
  resetPassword: (id: string) =>
    http.post<InitialCredential>(
      `${root}/users/${encodeURIComponent(id)}/reset-password`,
    ),
  setUserEnabled: (id: string, enabled: boolean) =>
    http.put<void>(`${root}/users/${encodeURIComponent(id)}/enabled`, {
      enabled,
    }),
  updateSelf: (displayName: string) =>
    http.patch<void>(`${root}/self`, { displayName }),
  changePassword: (input: { currentPassword: string; newPassword: string }) =>
    http.post<void>(`${root}/self/password`, input, {
      throwOnUnauthorized: true,
    }),
};
