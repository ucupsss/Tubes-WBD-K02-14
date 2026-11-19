// Klien API tunggal. SEMUA request ke backend lewat apiFetch supaya token CSRF selalu ikut
// dan format error seragam (lihat httpx.WriteError di backend).

export type FieldErrors = Record<string, string>

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: FieldErrors
  readonly reason: string

  constructor(status: number, code: string, message: string, fields: FieldErrors = {}, reason = '') {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.fields = fields
    this.reason = reason
  }
}

let csrfToken = ''

export const setCsrfToken = (token: string): void => {
  csrfToken = token
}

export const getCsrfToken = (): string => csrfToken

/** Mengambil token CSRF (pre-login atau milik session) dari server. */
export async function refreshCsrfToken(): Promise<string> {
  const res = await fetch('/api/csrf', { credentials: 'same-origin' })
  const body = (await res.json()) as { csrf_token?: string }
  csrfToken = body.csrf_token ?? ''
  return csrfToken
}

type Method = 'GET' | 'POST' | 'PUT' | 'DELETE'

export interface RequestOptions {
  method?: Method
  body?: unknown
  signal?: AbortSignal
}

/** Dipanggil saat server menjawab 401 untuk request yang butuh login (session habis). */
let onUnauthenticated: (() => void) | null = null
export const setUnauthenticatedHandler = (fn: (() => void) | null): void => {
  onUnauthenticated = fn
}

interface ErrorBody {
  error?: { code?: string; message?: string; details?: { fields?: FieldErrors; reason?: string } }
}

export function parseError(status: number, body: unknown): ApiError {
  const e = (body as ErrorBody | null)?.error
  return new ApiError(
    status,
    e?.code ?? 'internal',
    e?.message ?? 'Terjadi kesalahan pada server',
    e?.details?.fields ?? {},
    e?.details?.reason ?? '',
  )
}

export async function apiFetch<T = void>(path: string, opts: RequestOptions = {}): Promise<T> {
  const method = opts.method ?? 'GET'
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }
  if (method !== 'GET') {
    if (!csrfToken) {
      await refreshCsrfToken()
    }
    headers['X-CSRF-Token'] = csrfToken
  }

  let res: Response
  try {
    res = await fetch(path, {
      method,
      headers,
      credentials: 'same-origin',
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      signal: opts.signal,
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') {
      throw err
    }
    throw new ApiError(0, 'network', 'Tidak dapat terhubung ke server. Periksa koneksi Anda.')
  }

  if (res.status === 204) {
    return undefined as T
  }
  let body: unknown = null
  try {
    body = await res.json()
  } catch {
    // body kosong atau bukan JSON: ditangani di bawah
  }
  if (!res.ok) {
    const err = parseError(res.status, body)
    if (res.status === 401 && err.code === 'unauthenticated') {
      onUnauthenticated?.()
    }
    throw err
  }
  return body as T
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => apiFetch<T>(path, { signal }),
  post: <T = void>(path: string, body?: unknown) => apiFetch<T>(path, { method: 'POST', body }),
  put: <T = void>(path: string, body?: unknown) => apiFetch<T>(path, { method: 'PUT', body }),
  del: <T = void>(path: string) => apiFetch<T>(path, { method: 'DELETE' }),
}
