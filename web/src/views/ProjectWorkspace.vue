<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Box, ChevronRight, Folder, LayoutList, LayersIntersect, Plus, X } from '@vicons/tabler'
import AppButton from '../components/ui/AppButton.vue'
import PermissionGate from '../components/PermissionGate.vue'
import { usePermissions } from '../composables/usePermission'
import { useIdentityRequestScope } from '../composables/useAuthorizedRequestScope'
import { usePermissionStore } from '../stores/permissions'
import type { PermissionQuery } from '../api/access'
import { HttpError } from '../api/http'
import {
  assignWorkspaceApplication,
  createBusinessProject,
  createWorkspaceProduct,
  deleteBusinessProject,
  deleteWorkspaceProduct,
  listWorkspace,
  listWorkspaceApplications,
  updateBusinessProject,
  updateWorkspaceProduct,
  type WorkspaceApplication,
  type WorkspaceCardScope,
} from '../api/workspace'
import ProjectsContent from './ProjectsContent.vue'
import ProductReleasePanel from './ProductReleasePanel.vue'

type BusinessProject = Awaited<ReturnType<typeof listWorkspace>>['businessProjects'][number]
type Product = BusinessProject['products'][number]
type Selection = Pick<WorkspaceCardScope, 'kind' | 'id'>
type Operation =
  | { kind: 'newBusiness' }
  | { kind: 'renameBusiness' | 'deleteBusiness'; item: BusinessProject }
  | { kind: 'newProduct'; businessId: string }
  | { kind: 'renameProduct' | 'deleteProduct'; item: Product }
  | { kind: 'assign'; app: WorkspaceApplication }
  | { kind: 'attach'; product: Product }
const { t, d, te } = useI18n()
const route = useRoute()
const router = useRouter()
const permissions = usePermissionStore()
const team = permissions.activeTeam
const groups = ref<BusinessProject[]>([])
const loading = ref(true)
const loadError = ref(false)
const operation = shallowRef<Operation>()
const mutationError = ref('')
const mutationBusy = ref(false)
const actionStatus = ref('')
const formName = ref('')
const assignmentTarget = ref('')
const attachmentId = ref('')
const attachmentList = ref<WorkspaceApplication[]>([])
const attachmentPage = ref(1)
const attachmentPages = ref(1)
const attachmentLoading = ref(false)
const attachmentReadFailed = ref(false)
const attachmentPageSize = 20
const attachment = computed(() => attachmentList.value.find(application => application.id === attachmentId.value))
const validName = computed(() => Array.from(formName.value.trim()).length <= 128)
const directorySearch = ref('')
const currentApplication = shallowRef<WorkspaceApplication>()
const releasePanel = ref<InstanceType<typeof ProductReleasePanel>>()
const cards = ref<{
  openCreateModal: () => void
  loadProjects: () => Promise<void>
  openTriggerModal?: (application: WorkspaceApplication) => void
}>()
const scope = useIdentityRequestScope(() => {
  groups.value = []
  currentApplication.value = undefined
  operation.value = undefined
  attachmentList.value = []
  attachmentId.value = ''
  formName.value = ''
  editor.value?.close()
  drawer.value?.close()
}, () => true, undefined, () => false)

const searchText = computed(() => directorySearch.value.trim().toLocaleLowerCase())
const matches = (text: string) => text.toLocaleLowerCase().includes(searchText.value)
const visibleGroups = computed(() => groups.value.flatMap(group => {
  const products = !searchText.value || matches(group.name)
    ? group.products
    : group.products.filter(product => matches(product.name))
  return products.length || matches(group.name) ? [{ ...group, products }] : []
}))
const allProducts = computed(() => groups.value.flatMap(group => group.products))
const productNames = computed(() => Object.fromEntries(allProducts.value.map(product => [product.id, product.name])))
const selection = computed<Selection>(() => {
  const kind = route.query.workspaceScope
  const id = route.query.workspaceId
  if (kind === 'unassigned') return { kind }
  if (typeof id === 'string') {
    if (kind === 'business' && groups.value.some(group => group.id === id)) return { kind, id }
    if (kind === 'product' && allProducts.value.some(product => product.id === id)) return { kind, id }
  }
  return { kind: 'all' }
})
const selectedProduct = computed(() => selection.value.kind === 'product'
  ? allProducts.value.find(product => product.id === selection.value.id)
  : undefined)
const selectedBusiness = computed(() => groups.value.find(group => group.id === (
  selection.value.kind === 'business' ? selection.value.id : selectedProduct.value?.businessProjectId
)))
const heading = computed(() => selectedProduct.value?.name ?? selectedBusiness.value?.name ??
  t(selection.value.kind === 'unassigned' ? 'workspace.unassigned' : 'workspace.allApplications'))
const cardScope = computed<WorkspaceCardScope>(() => ({
  ...selection.value,
  label: selectedProduct.value && selectedBusiness.value
    ? selectedBusiness.value.name + ' / ' + selectedProduct.value.name
    : heading.value,
  productNames: productNames.value,
}))
const cardKey = computed(() => selection.value.kind + ':' + (selection.value.id ?? ''))
const detailTab = ref<'pipeline' | 'runs' | 'deployments'>('pipeline')
const detailTabs = ['pipeline', 'runs', 'deployments'] as const

function toSelection(kind: Selection['kind'], id?: string): void {
  if (!scope.active()) return
  const query: LocationQueryRaw = { ...route.query }
  delete query.workspaceScope
  delete query.workspaceId
  if (kind !== 'all') query.workspaceScope = kind
  if (id) query.workspaceId = id
  currentApplication.value = undefined
  closeDrawer()
  void router.push({ query })
}
function openApplication(application: WorkspaceApplication): void {
  if (!can([permission('project.read', application.id)])) return
  currentApplication.value = application
  detailTab.value = 'pipeline'
  void nextTick(() => detailHeading.value?.focus({ preventScroll: true }))
}
function exitApplication(): void {
  currentApplication.value = undefined
  const opener = detailOpener
  detailOpener = null
  void nextTick(() => { if (opener?.isConnected) opener.focus({ preventScroll: true }) })
}
function trackApplication(application: WorkspaceApplication): void {
  detailOpener = document.activeElement instanceof HTMLElement ? document.activeElement : null
  openApplication(application)
}
const detailHeading = ref<HTMLElement>()
let detailOpener: HTMLElement | null = null
watch(cardKey, () => { currentApplication.value = undefined })

let listing = 0
async function reload(): Promise<void> {
  if (!scope.active()) return
  const sequence = ++listing
  loading.value = true
  loadError.value = false
  try {
    const result = await scope.run(() => listWorkspace(scope.requestOptions))
    if (sequence !== listing) return
    groups.value = result.businessProjects
    if (route.query.workspaceScope && selection.value.kind === 'all') {
      const query: LocationQueryRaw = { ...route.query }
      delete query.workspaceScope
      delete query.workspaceId
      void router.replace({ query })
    }
  } catch {
    if (scope.active() && sequence === listing) {
      groups.value = []
      currentApplication.value = undefined
      loadError.value = true
    }
  } finally {
    if (sequence === listing) loading.value = false
  }
}

function permission(action: string, resourceId = '', create = false): PermissionQuery {
  return { action, resourceId, create }
}
function assignmentPermissions(app: WorkspaceApplication, target = ''): PermissionQuery[] {
  return [
    permission('project.edit', app.id),
    ...(app.productId ? [permission('product.manage', app.productId)] : []),
    ...(target && target !== app.productId ? [permission('product.manage', target)] : []),
  ]
}
function checks(action: Operation, target = ''): PermissionQuery[] {
  switch (action.kind) {
    case 'newBusiness': return [permission('business_project.create', '', true)]
    case 'renameBusiness':
    case 'deleteBusiness': return [permission('business_project.manage', action.item.id)]
    case 'newProduct': return [permission('business_project.manage', action.businessId), permission('product.create', '', true)]
    case 'renameProduct':
    case 'deleteProduct': return [permission('product.manage', action.item.id)]
    case 'assign': return assignmentPermissions(action.app, target)
    case 'attach': return attachment.value
      ? assignmentPermissions(attachment.value, action.product.id)
      : [permission('product.manage', action.product.id)]
  }
}
function can(queries: readonly PermissionQuery[]): boolean {
  return scope.active() && queries.every(query => permissions.allowed(team, query))
}
const { allowed: readApplication } = usePermissions(() => currentApplication.value
  ? [permission('project.read', currentApplication.value.id)] : [])
const { allowed: readLatestRun } = usePermissions(() => currentApplication.value?.latestRun
  ? [permission('run.read', currentApplication.value.latestRun.runId)] : [])
const { allowed: readDeploymentRun } = usePermissions(() => currentApplication.value?.productionDeployment
  ? [permission('run.read', currentApplication.value.productionDeployment.runId)] : [])
const applicationVisible = computed(() => !!currentApplication.value && scope.active() && readApplication.value)
const applicationCreatePermissions = computed(() => [
  permission('project.create', '', true),
  ...(selectedProduct.value ? [permission('product.manage', selectedProduct.value.id)] : []),
])
function createApplication(): void {
  if (can(applicationCreatePermissions.value)) cards.value?.openCreateModal()
}
const operationQueries = computed(() => operation.value ? checks(operation.value, assignmentTarget.value) : [])
const { allowed: operationAllowed } = usePermissions(operationQueries)
const isDelete = computed(() => operation.value?.kind === 'deleteBusiness' || operation.value?.kind === 'deleteProduct')
const operationName = computed(() => operation.value && 'item' in operation.value ? operation.value.item.name : '')
const canSubmit = computed(() => {
  const action = operation.value
  if (!action || mutationBusy.value || !operationAllowed.value) return false
  if (action.kind === 'assign') return assignmentTarget.value !== (action.app.productId ?? '')
  if (action.kind === 'attach') return Boolean(attachment.value && !attachmentLoading.value && !attachmentReadFailed.value)
  if (isDelete.value) return true
  return Boolean(validName.value && formName.value.trim() && formName.value.trim() !== operationName.value)
})

const editor = ref<HTMLDialogElement>()
const editorInput = ref<HTMLInputElement>()
const assignmentInput = ref<HTMLSelectElement>()
let editorOpener: HTMLElement | null = null
async function openEditor(action: Operation): Promise<void> {
  if (mutationBusy.value || !can(checks(action))) return
  editorOpener = drawer.value?.open
    ? drawerButton.value ?? null
    : document.activeElement instanceof HTMLElement ? document.activeElement : null
  closeDrawer()
  mutationError.value = ''
  actionStatus.value = ''
  formName.value = 'item' in action ? action.item.name : ''
  assignmentTarget.value = action.kind === 'assign' ? action.app.productId ?? '' : ''
  attachmentList.value = []
  attachmentId.value = ''
  attachmentReadFailed.value = false
  attachmentLoading.value = false
  attachmentPages.value = 1
  attachmentPage.value = 1
  operation.value = action
  if (action.kind === 'attach') void loadAttachmentPage(1)
  await nextTick()
  if (scope.active() && operation.value === action && editor.value && !editor.value.open) {
    editor.value.showModal()
    const input = editorInput.value ?? assignmentInput.value
    input?.focus()
  }
}
async function loadAttachmentPage(page: number): Promise<void> {
  const action = operation.value
  if (action?.kind !== 'attach' || attachmentLoading.value || mutationBusy.value ||
      !can([permission('product.manage', action.product.id)])) return
  attachmentLoading.value = true
  attachmentReadFailed.value = false
  attachmentId.value = ''
  try {
    const result = await scope.run(() => listWorkspaceApplications(
      { kind: 'all', label: '', productNames: {} }, page, attachmentPageSize, '', 'all', scope.requestOptions,
    ))
    if (operation.value !== action) return
    attachmentList.value = result.items.filter(app => app.productId !== action.product.id)
    attachmentPage.value = result.page
    attachmentPages.value = Math.ceil(result.total / result.pageSize)
  } catch {
    if (operation.value === action) attachmentReadFailed.value = true
  } finally {
    if (operation.value === action) attachmentLoading.value = false
  }
}
function closeEditor(): void {
  if (!mutationBusy.value) editor.value?.close()
}
function onEditorClosed(): void {
  operation.value = undefined
  attachmentList.value = []
  attachmentId.value = ''
  const opener = editorOpener
  editorOpener = null
  if (opener?.isConnected && opener.getClientRects().length) opener.focus({ preventScroll: true })
}
async function submitEditor(): Promise<void> {
  const action = operation.value
  const name = formName.value.trim()
  const target = assignmentTarget.value
  const appToAttach = attachment.value
  if (!action || !canSubmit.value || !can(checks(action, target))) return
  mutationBusy.value = true
  mutationError.value = ''
  const options = scope.requestOptions
  try {
    await scope.run(async () => {
      switch (action.kind) {
        case 'newBusiness': await createBusinessProject(name, options); break
        case 'renameBusiness': await updateBusinessProject(action.item.id, name, action.item.revision, options); break
        case 'deleteBusiness': await deleteBusinessProject(action.item.id, action.item.revision, options); break
        case 'newProduct': await createWorkspaceProduct(action.businessId, name, options); break
        case 'renameProduct': await updateWorkspaceProduct(action.item.id, name, action.item.revision, options); break
        case 'deleteProduct': await deleteWorkspaceProduct(action.item.id, action.item.revision, options); break
        case 'assign': await assignWorkspaceApplication(action.app.id, target || null, action.app.assignmentRevision, options); break
        case 'attach':
          if (appToAttach) await assignWorkspaceApplication(appToAttach.id, action.product.id, appToAttach.assignmentRevision, options)
          break
      }
    })
    actionStatus.value = 'saved'
    if (action.kind === 'assign' && currentApplication.value?.id === action.app.id) exitApplication()
    editor.value?.close()
    await Promise.all([reload(), cards.value?.loadProjects()])
  } catch (error) {
    if (!scope.active()) return
    const code = error instanceof HttpError ? error.apiError?.code : undefined
    if (code === 'workspace_not_empty') {
      mutationError.value = 'notEmpty'
    } else if (error instanceof HttpError && error.status === 409) {
      mutationError.value = 'conflict'
    } else {
      mutationError.value = 'saveFailed'
      if (error instanceof HttpError && error.status === 403) permissions.invalidate()
    }
  } finally {
    mutationBusy.value = false
  }
}
function reloadAfterConflict(): void {
  closeEditor()
  void Promise.all([reload(), cards.value?.loadProjects()])
}

const narrow = ref(false)
const drawerOpen = ref(false)
const drawer = ref<HTMLDialogElement>()
const drawerContent = ref<HTMLElement>()
const drawerButton = ref<HTMLButtonElement>()
const directoryInput = ref<HTMLInputElement>()
let media: MediaQueryList | undefined
function resize(event: MediaQueryListEvent): void {
  narrow.value = event.matches
  if (!event.matches) closeDrawer()
}
function showDrawer(): void {
  if (!narrow.value || !drawer.value || drawer.value.open) return
  drawer.value.showModal()
  drawerOpen.value = true
  directoryInput.value?.focus({ preventScroll: true })
}
function closeDrawer(): void {
  if (drawer.value?.open) drawer.value.close()
}
function onDrawerClosed(): void {
  drawerOpen.value = false
  if (narrow.value && !editor.value?.open) drawerButton.value?.focus({ preventScroll: true })
}

onMounted(() => {
  media = window.matchMedia('(max-width: 899px)')
  narrow.value = media.matches
  media.addEventListener('change', resize)
  void reload()
})
onBeforeUnmount(() => { media?.removeEventListener('change', resize) })

function runLink(runId: string) {
  return { name: 'run-detail', params: { id: runId } }
}
function runStatusLabel(value: string): string {
  const key = 'runStatus.' + (value === 'succeeded' ? 'success' : value)
  return te(key) ? t(key) : value
}
function date(value: string): string {
  const timestamp = new Date(value)
  return Number.isNaN(timestamp.getTime()) ? value : d(timestamp, { dateStyle: 'medium', timeStyle: 'short' })
}
</script>

<template>
  <div class="project-workspace">
    <div class="workspace-side">
      <Teleport :to="drawerContent ?? 'body'" :disabled="!narrow || !drawerContent">
        <nav class="workspace-directory" :aria-label="t('workspace.directory')">
          <header class="workspace-directory__header">
            <h2>{{ t('workspace.directory') }}</h2>
            <PermissionGate :query="permission('business_project.create', '', true)">
              <AppButton
                class="workspace-icon"
                :title="t('workspace.newBusiness')"
                :aria-label="t('workspace.newBusiness')"
                @click="openEditor({ kind: 'newBusiness' })"
              ><Plus aria-hidden="true" /></AppButton>
            </PermissionGate>
          </header>
          <input
            ref="directoryInput"
            v-model="directorySearch"
            class="workspace-input"
            type="search"
            :aria-label="t('workspace.searchDirectory')"
            :placeholder="t('workspace.searchDirectory')"
          />
          <div class="workspace-directory__scroll">
            <button
              class="workspace-tree-item"
              :aria-current="selection.kind === 'all' ? 'page' : undefined"
              type="button"
              @click="toSelection('all')"
            ><LayersIntersect aria-hidden="true" />{{ t('workspace.allApplications') }}</button>
            <details v-for="group in visibleGroups" :key="group.id" :open="!!searchText || selectedBusiness?.id === group.id">
              <summary class="workspace-tree-item workspace-tree-group">
                <ChevronRight class="workspace-tree-arrow" aria-hidden="true" />
                <Folder aria-hidden="true" />
                <span :title="group.name">{{ group.name }}</span>
                <small>{{ group.products.length }}</small>
              </summary>
              <div class="workspace-tree-children">
                <button
                  class="workspace-tree-item"
                  type="button"
                  :aria-current="selection.kind === 'business' && selection.id === group.id ? 'page' : undefined"
                  :aria-label="t('workspace.businessProducts', { name: group.name })"
                  @click="toSelection('business', group.id)"
                >{{ t('workspace.allProducts') }}</button>
                <button
                  v-for="product in group.products"
                  :key="product.id"
                  class="workspace-tree-item"
                  type="button"
                  :aria-current="selection.kind === 'product' && selection.id === product.id ? 'page' : undefined"
                  @click="toSelection('product', product.id)"
                ><Box aria-hidden="true" /><span :title="product.name">{{ product.name }}</span></button>
              </div>
            </details>
            <button
              v-if="matches(t('workspace.unassigned'))"
              class="workspace-tree-item"
              type="button"
              :aria-current="selection.kind === 'unassigned' ? 'page' : undefined"
              @click="toSelection('unassigned')"
            ><Folder aria-hidden="true" />{{ t('workspace.unassigned') }}</button>
            <p v-if="searchText && !visibleGroups.length" class="workspace-muted">{{ t('workspace.noDirectoryMatches') }}</p>
          </div>
        </nav>
      </Teleport>
    </div>

    <div class="workspace-content">
      <nav class="workspace-crumbs" :aria-label="t('workspace.breadcrumb')">
        <button
          ref="drawerButton"
          class="workspace-drawer-toggle"
          type="button"
          aria-controls="workspace-directory-drawer"
          :aria-expanded="drawerOpen"
          @click="showDrawer"
        ><LayoutList aria-hidden="true" />{{ t('workspace.directory') }}</button>
        <button
          v-if="selection.kind !== 'all' || currentApplication"
          class="workspace-crumb"
          type="button"
          @click="toSelection('all')"
        >{{ t('workspace.allApplications') }}</button>
        <template v-if="selectedBusiness">
          <ChevronRight aria-hidden="true" />
          <span :aria-current="!selectedProduct && !currentApplication ? 'page' : undefined">{{ selectedBusiness.name }}</span>
        </template>
        <template v-if="selectedProduct && selectedBusiness">
          <ChevronRight aria-hidden="true" />
          <button class="workspace-crumb" type="button" @click="toSelection('business', selectedBusiness.id)">
            {{ t('workspace.allProducts') }}
          </button>
          <ChevronRight aria-hidden="true" />
          <button v-if="currentApplication" class="workspace-crumb" type="button" @click="exitApplication">{{ selectedProduct.name }}</button>
          <span v-else aria-current="page">{{ selectedProduct.name }}</span>
        </template>
        <template v-else-if="selection.kind === 'unassigned'">
          <ChevronRight aria-hidden="true" />
          <span>{{ t('workspace.unassigned') }}</span>
        </template>
      </nav>

      <div v-if="loadError" role="alert" class="workspace-panel workspace-error">
        <p>{{ t('workspace.loadFailed') }}</p>
        <AppButton @click="reload">{{ t('settingsAccount.retry') }}</AppButton>
      </div>
      <p v-else-if="loading && !groups.length" class="workspace-panel workspace-muted" role="status">{{ t('workspace.loading') }}</p>

      <template v-if="!loadError && (!loading || groups.length)">
        <div v-show="!applicationVisible" class="workspace-list-header">
          <h1>{{ heading }}</h1>
          <div class="workspace-actions">
            <PermissionGate v-if="selectedProduct" :query="permission('product.manage', selectedProduct.id)">
              <AppButton @click="openEditor({ kind: 'attach', product: selectedProduct })">{{ t('workspace.attach') }}</AppButton>
              <AppButton @click="openEditor({ kind: 'renameProduct', item: selectedProduct })">{{ t('workspace.renameProduct') }}</AppButton>
              <AppButton variant="danger" @click="openEditor({ kind: 'deleteProduct', item: selectedProduct })">{{ t('workspace.deleteProduct') }}</AppButton>
            </PermissionGate>
            <template v-else-if="selectedBusiness">
              <PermissionGate :query="permission('business_project.manage', selectedBusiness.id)">
                <AppButton @click="openEditor({ kind: 'renameBusiness', item: selectedBusiness })">{{ t('workspace.renameBusiness') }}</AppButton>
                <AppButton variant="danger" @click="openEditor({ kind: 'deleteBusiness', item: selectedBusiness })">{{ t('workspace.deleteBusiness') }}</AppButton>
              </PermissionGate>
              <PermissionGate :query="checks({ kind: 'newProduct', businessId: selectedBusiness.id })">
                <AppButton variant="primary" @click="openEditor({ kind: 'newProduct', businessId: selectedBusiness.id })">{{ t('workspace.newProduct') }}</AppButton>
              </PermissionGate>
            </template>
            <AppButton v-if="selectedProduct && releasePanel" @click="releasePanel.open()">{{ t('workspace.release.open') }}</AppButton>
            <PermissionGate v-if="!selectedBusiness || selectedProduct" :query="applicationCreatePermissions">
              <AppButton variant="primary" @click="createApplication"><Plus aria-hidden="true" />{{ t('workspace.newApplication') }}</AppButton>
            </PermissionGate>
          </div>
        </div>
        <section v-if="selectedBusiness && !selectedProduct && !applicationVisible" class="workspace-panel" aria-labelledby="workspace-all-products-heading">
          <h2 id="workspace-all-products-heading">{{ t('workspace.allProducts') }}</h2>
          <ul v-if="selectedBusiness.products.length" class="workspace-product-list">
            <li v-for="product in selectedBusiness.products" :key="product.id">
              <button class="workspace-product-button" type="button" @click="toSelection('product', product.id)">
                <Box aria-hidden="true" />
                <span>{{ product.name }}</span>
                <ChevronRight aria-hidden="true" />
              </button>
            </li>
          </ul>
          <p v-else class="workspace-muted">{{ t('workspace.noProducts') }}</p>
        </section>

        <section v-if="currentApplication && applicationVisible" aria-labelledby="workspace-application-title">
          <div class="workspace-list-header">
            <div class="workspace-detail-heading">
              <button class="workspace-crumb" type="button" @click="exitApplication">{{ t('workspace.backToApplications') }}</button>
              <h1 id="workspace-application-title" ref="detailHeading" tabindex="-1">{{ currentApplication.name }}</h1>
            </div>
            <div class="workspace-actions">
              <PermissionGate :query="assignmentPermissions(currentApplication, '')">
                <AppButton @click="openEditor({ kind: 'assign', app: currentApplication })">{{ t('workspace.assign') }}</AppButton>
              </PermissionGate>
              <PermissionGate :query="permission('run.start', currentApplication.id, true)">
                <AppButton v-if="cards?.openTriggerModal" variant="primary" @click="cards.openTriggerModal(currentApplication)">{{ t('workspace.runPipeline') }}</AppButton>
              </PermissionGate>
            </div>
          </div>
          <dl class="workspace-panel workspace-app-metadata">
            <div><dt>{{ t('projects.fieldRepo') }}</dt><dd class="mono">{{ currentApplication.repoUrl }}</dd></div>
            <div><dt>{{ t('projects.fieldDefaultBranch') }}</dt><dd class="mono">{{ currentApplication.defaultBranch }}</dd></div>
          </dl>
          <div class="workspace-panel">
            <div class="workspace-detail-tabs" role="group" :aria-label="t('workspace.applicationSections')">
              <AppButton
                v-for="tab in detailTabs"
                :key="tab"
                :variant="tab === detailTab ? 'default' : 'ghost'"
                :aria-pressed="tab === detailTab"
                @click="detailTab = tab"
              >{{ t('workspace.' + tab) }}</AppButton>
            </div>
            <div v-if="detailTab === 'pipeline'" class="workspace-fact">
              <p>{{ t('workspace.pipelineDescription') }}</p>
              <PermissionGate :query="permission('pipeline.read', currentApplication.id)">
                <RouterLink
                  class="workspace-crumb workspace-pipeline-link"
                  :to="{ name: 'project-pipeline', params: { id: currentApplication.id } }"
                >{{ t('workspace.openPipeline') }}</RouterLink>
              </PermissionGate>
            </div>
            <template v-else-if="detailTab === 'runs'">
              <div v-if="currentApplication.latestRun && readLatestRun" class="workspace-fact">
                <span>{{ runStatusLabel(currentApplication.latestRun.status) }} · {{ date(currentApplication.latestRun.createdAt) }}</span>
                <span class="mono">{{ currentApplication.latestRun.commit }}</span>
                <RouterLink class="workspace-crumb" :to="runLink(currentApplication.latestRun.runId)">{{ t('workspace.openRun') }}</RouterLink>
              </div>
              <p v-else class="workspace-muted">{{ t('workspace.noRuns') }}</p>
            </template>
            <template v-else>
              <div v-if="currentApplication.productionDeployment && readDeploymentRun" class="workspace-fact">
                <span>{{ currentApplication.productionDeployment.environment }} · {{ date(currentApplication.productionDeployment.deployedAt) }}</span>
                <span class="mono">{{ currentApplication.productionDeployment.commit }}</span>
                <RouterLink class="workspace-crumb" :to="runLink(currentApplication.productionDeployment.runId)">{{ t('workspace.openRun') }}</RouterLink>
              </div>
              <p v-else class="workspace-muted">{{ t('workspace.undeployed') }}</p>
            </template>
          </div>
        </section>

        <ProjectsContent
          v-show="!applicationVisible"
          :key="cardKey"
          ref="cards"
          :workspace="cardScope"
          :workspace-can-create="can(applicationCreatePermissions)"
          :workspace-assignment-permissions="assignmentPermissions"
          @open-application="trackApplication"
          @open-product="toSelection('product', $event)"
          @assign-application="openEditor({ kind: 'assign', app: $event })"
          @workspace-changed="reload"
        />
        <PermissionGate v-if="selectedProduct && !applicationVisible" :query="permission('product.read', selectedProduct.id)">
          <ProductReleasePanel
            :key="selectedProduct.id"
            ref="releasePanel"
            :product-id="selectedProduct.id"
            :product-name="selectedProduct.name"
          />
        </PermissionGate>
      </template>

      <p v-if="actionStatus" class="workspace-muted" role="status">{{ t('workspace.' + actionStatus) }}</p>
    </div>
  </div>

  <Teleport to="body">
    <dialog
      id="workspace-directory-drawer"
      ref="drawer"
      class="workspace-drawer"
      :aria-label="t('workspace.directory')"
      @click.self="closeDrawer"
      @close="onDrawerClosed"
    >
      <div class="workspace-drawer__body">
        <div class="workspace-drawer__close">
          <AppButton
            class="workspace-icon"
            :title="t('projects.closeDialog')"
            :aria-label="t('projects.closeDialog')"
            @click="closeDrawer"
          ><X aria-hidden="true" /></AppButton>
        </div>
        <div ref="drawerContent" class="workspace-drawer__slot" />
      </div>
    </dialog>

    <dialog
      ref="editor"
      class="workspace-editor"
      aria-labelledby="workspace-editor-heading"
      @close="onEditorClosed"
      @cancel.prevent="closeEditor"
    >
      <form v-if="operation" class="workspace-editor__form" @submit.prevent="submitEditor">
        <h2 id="workspace-editor-heading">{{ t('workspace.' + operation.kind) }}</h2>
        <p v-if="isDelete">{{ t('workspace.deletePrompt', { name: operationName }) }}</p>
        <template v-else-if="operation.kind === 'assign'">
          <label for="workspace-assignment">{{ t('workspace.assignmentProduct', { name: operation.app.name }) }}</label>
          <select id="workspace-assignment" ref="assignmentInput" v-model="assignmentTarget" class="workspace-input" :disabled="mutationBusy">
            <option value="">{{ t('workspace.unassigned') }}</option>
            <optgroup v-for="group in groups" :key="group.id" :label="group.name">
              <PermissionGate v-for="product in group.products" :key="product.id" :query="permission('product.manage', product.id)">
                <option :value="product.id">{{ product.name }}</option>
              </PermissionGate>
            </optgroup>
          </select>
        </template>
        <template v-else-if="operation.kind === 'attach'">
          <label for="workspace-attachment">{{ t('workspace.chooseApplication') }}</label>
          <select
            id="workspace-attachment"
            ref="assignmentInput"
            v-model="attachmentId"
            class="workspace-input"
            :disabled="attachmentLoading || attachmentReadFailed || mutationBusy"
          >
            <option value="">{{ t('workspace.chooseApplication') }}</option>
            <PermissionGate
              v-for="app in attachmentList"
              :key="app.id"
              :query="assignmentPermissions(app, operation.product.id)"
            >
              <option :value="app.id">{{ app.name }} · {{ app.productId ? (productNames[app.productId] ?? t('workspace.owner')) : t('workspace.unassigned') }}</option>
            </PermissionGate>
          </select>
          <p v-if="attachmentReadFailed" class="workspace-error" role="alert">
            {{ t('workspace.loadApplicationsFailed') }}
            <button class="workspace-crumb" type="button" @click="loadAttachmentPage(attachmentPage)">{{ t('workspace.refresh') }}</button>
          </p>
          <p v-else-if="attachmentLoading" class="workspace-muted" role="status">{{ t('workspace.loadingApplications') }}</p>
          <p v-else-if="!attachmentList.length" class="workspace-muted">{{ t('workspace.noApplications') }}</p>
          <div v-if="attachmentPages > 1" class="workspace-actions">
            <AppButton :disabled="attachmentLoading || mutationBusy || attachmentPage <= 1" @click="loadAttachmentPage(attachmentPage - 1)">{{ t('workspace.previous') }}</AppButton>
            <span>{{ t('workspace.page', { page: attachmentPage }) }}</span>
            <AppButton :disabled="attachmentLoading || mutationBusy || attachmentPage >= attachmentPages" @click="loadAttachmentPage(attachmentPage + 1)">{{ t('workspace.next') }}</AppButton>
          </div>
        </template>
        <template v-else>
          <label for="workspace-name">{{ t('workspace.name') }}</label>
          <input id="workspace-name" ref="editorInput" v-model="formName" class="workspace-input" maxlength="256" required :disabled="mutationBusy" :aria-invalid="!validName || undefined" aria-describedby="workspace-name-limit" />
          <p id="workspace-name-limit" :class="validName ? 'workspace-muted' : 'workspace-error'">{{ t('workspace.nameLimit', { limit: 128 }) }}</p>
        </template>
        <p v-if="mutationError" class="workspace-error" role="alert">{{ t('workspace.' + mutationError) }}</p>
        <div class="workspace-actions workspace-editor__actions">
          <AppButton :disabled="mutationBusy" @click="closeEditor">{{ t('projects.cancel') }}</AppButton>
          <AppButton v-if="mutationError === 'conflict'" @click="reloadAfterConflict">{{ t('workspace.refresh') }}</AppButton>
          <AppButton
            v-else
            type="submit"
            :variant="isDelete ? 'danger' : 'primary'"
            :disabled="!canSubmit"
            :loading="mutationBusy"
          >{{ t(isDelete ? 'workspace.delete' : 'projects.save') }}</AppButton>
        </div>
      </form>
    </dialog>
  </Teleport>
</template>
<style scoped>
.project-workspace {
  display: grid;
  grid-template-columns: minmax(190px, 230px) minmax(0, 1fr);
  gap: 16px;
  align-items: start;
  min-width: 0;
}
.workspace-side {
  position: sticky;
  inset-block-start: 0;
}
.workspace-content {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 14px;
  min-width: 0;
}
.workspace-panel,
.workspace-directory {
  padding: 14px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  background: var(--color-card);
}
.workspace-panel > h2,
.workspace-directory h2,
.workspace-editor h2 {
  margin: 0;
  font-size: 1rem;
  overflow-wrap: anywhere;
}
.workspace-directory {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.workspace-directory__header,
.workspace-list-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-width: 0;
}
.workspace-list-header {
  flex-wrap: wrap;
}
.workspace-list-header h1 {
  margin: 0;
  font-size: clamp(1.15rem, 1rem + 0.55vw, 1.5rem);
  overflow-wrap: anywhere;
}
.workspace-input {
  width: 100%;
  min-width: 0;
  min-height: 38px;
  padding: 7px 10px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  background: var(--color-inset);
  color: var(--color-text);
  font: inherit;
}
.workspace-directory__scroll {
  display: grid;
  gap: 3px;
  max-block-size: calc(100vh - 180px);
  max-block-size: calc(100dvh - 180px);
  min-block-size: 44px;
  padding: 2px;
  overflow: auto;
  overscroll-behavior: contain;
}
.workspace-tree-item,
.workspace-product-button,
.workspace-crumb,
.workspace-drawer-toggle {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
  min-height: 36px;
  padding: 5px 8px;
  border: 0;
  border-radius: var(--rounded);
  background: transparent;
  color: var(--color-text);
  font: inherit;
  text-align: start;
  cursor: pointer;
}
.workspace-tree-item,
.workspace-product-button {
  width: 100%;
}
.workspace-tree-item:hover,
.workspace-product-button:hover,
.workspace-crumb:hover,
.workspace-drawer-toggle:hover {
  background: var(--color-card-2);
}
.workspace-tree-item[aria-current='page'],
.workspace-detail-tabs [aria-pressed='true'] {
  background: var(--color-primary-soft);
  color: var(--color-primary);
  font-weight: 600;
}
.workspace-tree-item span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.workspace-tree-item small {
  margin-inline-start: auto;
  color: var(--color-dim);
}
.workspace-tree-group::-webkit-details-marker {
  display: none;
}
details[open] .workspace-tree-arrow {
  transform: rotate(90deg);
}
.workspace-tree-children {
  padding-inline-start: 17px;
}
.workspace-crumbs,
.workspace-actions,
.workspace-detail-tabs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.workspace-crumbs {
  gap: 4px;
  color: var(--color-dim);
}
.workspace-crumbs span {
  min-width: 0;
  overflow-wrap: anywhere;
}
.workspace-crumb {
  color: var(--color-primary);
}
.workspace-crumb:focus-visible,
.workspace-drawer-toggle:focus-visible,
.workspace-tree-item:focus-visible,
.workspace-product-button:focus-visible,
.workspace-input:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 1px;
}
.workspace-product-list {
  display: grid;
  gap: 5px;
  padding: 0;
  margin: 10px 0 0;
  list-style: none;
}
.workspace-product-button {
  border: 1px solid var(--color-border);
  background: var(--color-inset);
}
.workspace-product-button span {
  flex: 1;
  overflow-wrap: anywhere;
}
.workspace-directory svg,
.workspace-content svg,
.workspace-icon svg {
  width: 16px;
  height: 16px;
  flex: none;
}
.workspace-icon {
  width: 38px;
  padding: 0;
  flex: none;
}
.workspace-drawer-toggle {
  display: none;
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
}
.workspace-drawer,
.workspace-editor {
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-text);
  box-shadow: var(--shadow-modal);
}
.workspace-editor::backdrop,
.workspace-drawer::backdrop {
  background: rgb(10 16 28 / 58%);
}
.workspace-drawer {
  position: fixed;
  inset: 0 auto 0 0;
  margin: 0;
  width: min(330px, calc(100% - 26px));
  height: 100%;
  max-height: 100%;
  padding: 0;
  overflow: hidden;
  border-radius: 0 var(--rounded-card) var(--rounded-card) 0;
}
.workspace-drawer__body {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.workspace-drawer__close {
  flex: none;
  display: flex;
  justify-content: end;
  padding: 8px 12px 0;
}
.workspace-drawer__slot,
.workspace-drawer__slot > .workspace-directory {
  flex: 1;
  min-height: 0;
}
.workspace-drawer__slot {
  display: flex;
}
.workspace-drawer__slot > .workspace-directory {
  border: 0;
}
.workspace-drawer__slot .workspace-directory__scroll {
  max-block-size: none;
}
.workspace-editor {
  position: fixed;
  inset: 0;
  margin: auto;
  width: min(460px, calc(100% - 24px));
  max-block-size: calc(100vh - 24px);
  max-block-size: calc(100dvh - 24px);
  padding: 16px;
  overflow: auto;
  border-radius: var(--rounded-card);
}
.workspace-editor__form {
  display: grid;
  gap: 12px;
  overflow-wrap: anywhere;
}
.workspace-editor__actions {
  justify-content: end;
  margin-block-start: 5px;
}
:global([data-theme="dark"] .project-workspace .app-btn--primary),
:global([data-theme="dark"] .workspace-editor .app-btn--primary) {
  color: var(--color-bg);
}
.workspace-detail-heading {
  min-width: 0;
}
.workspace-app-metadata,
.workspace-fact {
  display: grid;
  gap: 8px;
  overflow-wrap: anywhere;
}
.workspace-app-metadata {
  margin-block: 10px;
}
.workspace-app-metadata dt,
.workspace-muted {
  color: var(--color-dim);
  font-size: 0.875rem;
}
.workspace-app-metadata dd {
  margin: 0;
}
.workspace-detail-tabs {
  margin-block-end: 10px;
}
.workspace-pipeline-link {
  justify-self: start;
  border: 1px solid var(--color-border-strong);
}
.workspace-error {
  color: var(--color-red);
}
@media (max-width: 899px) {
  .project-workspace {
    display: block;
  }
  .workspace-side {
    display: none;
  }
  .workspace-drawer-toggle {
    display: inline-flex;
  }
}
@media (max-width: 600px), (pointer: coarse) {
  .workspace-drawer-toggle,
  .workspace-tree-item,
  .workspace-product-button,
  .workspace-crumb,
  .workspace-input,
  .workspace-icon,
  .workspace-actions :deep(button),
  .workspace-detail-tabs :deep(button) {
    min-height: 44px;
  }
  .workspace-icon {
    width: 44px;
  }
  .workspace-input {
    font-size: 16px;
  }
}
</style>
