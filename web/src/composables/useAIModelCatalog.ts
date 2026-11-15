import { computed, onBeforeUnmount, shallowRef, watch, type Ref } from 'vue'
import { listAIModels, type AIProvider } from '../api/aiSettings'
import { HttpError } from '../api/http'
import type { AuthorizedRequestScope } from './useAuthorizedRequestScope'

export function useAIModelCatalog(config: {
  provider: Ref<AIProvider>
  baseUrl: Ref<string>
  apiKey: Ref<string>
  ready: Ref<boolean>
  hasExistingKey: Ref<boolean>
  unavailable: () => string
  scope?: AuthorizedRequestScope
}) {
  const options = shallowRef<{ value: string; label: string }[]>([])
  const loading = shallowRef(false)
  const error = shallowRef('')
  let generation = 0
  let controller: AbortController | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  let disposed = false
  const canFetch = computed(() => {
    if (!config.ready.value || !config.provider.value) return false
    try {
      const url = new URL(config.baseUrl.value.trim())
      if (
        !['https:', 'http:'].includes(url.protocol) ||
        url.username ||
        url.password ||
        url.search ||
        url.hash
      )
        return false
    } catch {
      return false
    }
    return (
      config.provider.value === 'ollama' ||
      !!config.apiKey.value.trim() ||
      config.hasExistingKey.value
    )
  })

  function invalidate() {
    clearTimeout(timer)
    timer = undefined
    generation++
    controller?.abort()
    loading.value = false
    error.value = ''
    options.value = []
  }

  async function refresh() {
    clearTimeout(timer)
    timer = undefined
    if (disposed || !canFetch.value || (config.scope && !config.scope.active())) return
    const current = ++generation
    controller?.abort()
    controller = new AbortController()
    loading.value = true
    error.value = ''
    try {
      const request = () => listAIModels(
        {
          provider: config.provider.value,
          baseUrl: config.baseUrl.value.trim(),
          ...(config.apiKey.value ? { apiKey: config.apiKey.value } : {}),
        },
        controller!.signal,
        config.scope?.requestOptions,
      )
      const result = await (config.scope ? config.scope.run(request) : request())
      if (disposed || current !== generation || (config.scope && !config.scope.active())) return
      options.value = result.ok
        ? result.items.map((model) => ({
            value: model.id,
            label:
              model.name === model.id
                ? model.id
                : model.name + ' · ' + model.id,
          }))
        : []
      if (!result.ok) error.value = result.error || config.unavailable()
    } catch (err) {
      if (!disposed && current === generation) {
        error.value =
          err instanceof HttpError
            ? err.apiError?.message || config.unavailable()
            : config.unavailable()
      }
    } finally {
      if (!disposed && current === generation) loading.value = false
    }
  }

  watch(
    [config.provider, config.baseUrl, config.apiKey, canFetch],
    () => {
      invalidate()
      if (canFetch.value) timer = setTimeout(() => void refresh(), 600)
    },
    { immediate: true, flush: 'sync' },
  )

  onBeforeUnmount(() => {
    disposed = true
    invalidate()
  })
  return { options, loading, error, refresh, canFetch }
}
