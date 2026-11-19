import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, apiFetch, parseError, setCsrfToken, setUnauthenticatedHandler } from './client'

type FetchArgs = [string, RequestInit]

function mockFetch(responses: Array<{ status: number; body?: unknown }>) {
  const calls: FetchArgs[] = []
  const fn = vi.fn(async (url: string, init: RequestInit) => {
    calls.push([url, init])
    const r = responses.shift()
    if (!r) {
      throw new Error('fetch dipanggil lebih banyak dari yang diharapkan')
    }
    return new Response(r.body === undefined ? null : JSON.stringify(r.body), {
      status: r.status,
      headers: { 'Content-Type': 'application/json' },
    })
  })
  vi.stubGlobal('fetch', fn)
  return calls
}

beforeEach(() => {
  setCsrfToken('')
  setUnauthenticatedHandler(null)
})
afterEach(() => vi.unstubAllGlobals())

describe('parseError', () => {
  it('memetakan bentuk error API', () => {
    const err = parseError(400, {
      error: { code: 'invalid', message: 'Input tidak valid', details: { fields: { name: 'wajib' }, reason: 'x' } },
    })
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(400)
    expect(err.code).toBe('invalid')
    expect(err.fields).toEqual({ name: 'wajib' })
    expect(err.reason).toBe('x')
  })

  it('memakai nilai aman bila body bukan error API', () => {
    const err = parseError(500, null)
    expect(err.code).toBe('internal')
    expect(err.message).toBe('Terjadi kesalahan pada server')
  })
})

describe('apiFetch', () => {
  it('GET tidak mengirim token CSRF', async () => {
    const calls = mockFetch([{ status: 200, body: { ok: 1 } }])
    const out = await apiFetch<{ ok: number }>('/api/x')
    expect(out).toEqual({ ok: 1 })
    expect((calls[0][1].headers as Record<string, string>)['X-CSRF-Token']).toBeUndefined()
  })

  it('mutasi mengirim header CSRF dan body JSON', async () => {
    setCsrfToken('tok123')
    const calls = mockFetch([{ status: 201, body: { id: 5 } }])
    const out = await apiFetch<{ id: number }>('/api/admin/groups', { method: 'POST', body: { name: 'dev' } })
    expect(out.id).toBe(5)
    const headers = calls[0][1].headers as Record<string, string>
    expect(headers['X-CSRF-Token']).toBe('tok123')
    expect(headers['Content-Type']).toBe('application/json')
    expect(calls[0][1].body).toBe('{"name":"dev"}')
  })

  it('mutasi tanpa token mengambilnya dulu dari /api/csrf', async () => {
    const calls = mockFetch([
      { status: 200, body: { csrf_token: 'fresh' } },
      { status: 204 },
    ])
    await apiFetch('/api/auth/logout', { method: 'POST' })
    expect(calls[0][0]).toBe('/api/csrf')
    expect((calls[1][1].headers as Record<string, string>)['X-CSRF-Token']).toBe('fresh')
  })

  it('204 mengembalikan undefined', async () => {
    setCsrfToken('t')
    mockFetch([{ status: 204 }])
    await expect(apiFetch('/api/x', { method: 'DELETE' })).resolves.toBeUndefined()
  })

  it('error API dilempar sebagai ApiError dengan field', async () => {
    setCsrfToken('t')
    mockFetch([
      { status: 400, body: { error: { code: 'invalid', message: 'Input tidak valid', details: { fields: { username: 'salah' } } } } },
    ])
    const err = await apiFetch('/api/admin/users', { method: 'POST', body: {} }).catch(e => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).fields.username).toBe('salah')
  })

  it('401 unauthenticated memanggil handler session habis', async () => {
    const handler = vi.fn()
    setUnauthenticatedHandler(handler)
    mockFetch([{ status: 401, body: { error: { code: 'unauthenticated', message: 'Silakan login' } } }])
    await expect(apiFetch('/api/me')).rejects.toBeInstanceOf(ApiError)
    expect(handler).toHaveBeenCalledOnce()
  })

  it('kegagalan jaringan menjadi ApiError network', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    const err = await apiFetch('/api/x').catch(e => e)
    expect((err as ApiError).code).toBe('network')
  })
})
