/**
 * Fetch wrapper for devopsTool API.
 *
 * - Reads devops_csrf cookie and attaches X-CSRF-Token header on write methods
 *   (POST / PUT / PATCH / DELETE) — "double-submit cookie" CSRF protection.
 * - On 401, redirects to /login (preserving current URL as ?redirect=).
 * - Parses the canonical error envelope { error: { code, message } }.
 */

export interface ApiError {
  code: string
  message: string
}

export class HttpError extends Error {
  constructor(
    public readonly status: number,
    public readonly apiError: ApiError | null,
    message: string,
  ) {
    super(message)
    this.name = 'HttpError'
  }
}

/** Read a cookie value by name. Returns empty string if not found. */
function getCookie(name: string): string {
  const match = document.cookie.split(';').find((c) => c.trim().startsWith(name + '='))
  return match ? decodeURIComponent(match.trim().slice(name.length + 1)) : ''
}

const WRITE_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

/** Guard against concurrent 401 responses each triggering a redirect. */
let redirectingToLogin = false

async function request<T>(
  url: string,
  options: RequestInit = {},
): Promise<T> {
  const method = (options.method ?? 'GET').toUpperCase()
  const headers = new Headers(options.headers)

  if (!headers.has('Content-Type') && !(options.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json')
  }

  if (WRITE_METHODS.has(method)) {
    const csrfToken = getCookie('devops_csrf')
    if (csrfToken) {
      headers.set('X-CSRF-Token', csrfToken)
    }
  }

  let response: Response
  try {
    response = await fetch(url, { ...options, headers, credentials: 'same-origin' })
  } catch (err) {
    // Network error — backend may not be up yet
    throw new HttpError(0, null, err instanceof Error ? err.message : 'Network error')
  }

  // Auth endpoints (login / session / logout) own their 401 handling:
  // the login form shows an inline error, the route guard treats it as
  // "not logged in". Only redirect for 401s on *other* protected calls.
  const isAuthEndpoint = url.includes('/api/auth/')

  if (response.status === 401 && !isAuthEndpoint) {
    // Not logged in — redirect to login exactly once even if multiple
    // concurrent requests all return 401 simultaneously.
    if (!redirectingToLogin) {
      redirectingToLogin = true
      const currentPath =
        location.pathname +
        (location.search ? location.search : '') +
        (location.hash ? location.hash : '')
      const redirectTo = encodeURIComponent(currentPath)
      location.replace(`/login?redirect=${redirectTo}`)
    }
    // Return a never-resolving promise so callers don't see undefined
    return new Promise<never>(() => undefined)
  }

  if (response.status === 204) {
    return undefined as T
  }

  let body: unknown
  const ct = response.headers.get('content-type') ?? ''
  if (ct.includes('application/json')) {
    body = await response.json()
  } else {
    body = await response.text()
  }

  if (!response.ok) {
    // Try to extract structured error envelope
    let apiError: ApiError | null = null
    if (typeof body === 'object' && body !== null && 'error' in body) {
      const e = (body as { error: unknown }).error
      if (typeof e === 'object' && e !== null && 'code' in e && 'message' in e) {
        apiError = e as ApiError
      }
    }
    throw new HttpError(
      response.status,
      apiError,
      apiError?.message ?? `HTTP ${response.status}`,
    )
  }

  return body as T
}

export const http = {
  get<T>(url: string, options?: RequestInit): Promise<T> {
    return request<T>(url, { ...options, method: 'GET' })
  },
  post<T>(url: string, data?: unknown, options?: RequestInit): Promise<T> {
    return request<T>(url, {
      ...options,
      method: 'POST',
      body: data !== undefined ? JSON.stringify(data) : undefined,
    })
  },
  put<T>(url: string, data?: unknown, options?: RequestInit): Promise<T> {
    return request<T>(url, {
      ...options,
      method: 'PUT',
      body: data !== undefined ? JSON.stringify(data) : undefined,
    })
  },
  patch<T>(url: string, data?: unknown, options?: RequestInit): Promise<T> {
    return request<T>(url, {
      ...options,
      method: 'PATCH',
      body: data !== undefined ? JSON.stringify(data) : undefined,
    })
  },
  delete<T>(url: string, options?: RequestInit): Promise<T> {
    return request<T>(url, { ...options, method: 'DELETE' })
  },
}
