import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { createContext, useCallback, useContext, useEffect, useMemo, type ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { api, ApiError, refreshCsrfToken, setCsrfToken, setUnauthenticatedHandler } from '../api/client'
import { keys } from '../api/hooks'
import type { LoginResult, Me } from '../api/types'

interface AuthState {
  me: Me | null
  isLoading: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

async function fetchMe(): Promise<Me | null> {
  try {
    const me = await api.get<Me>('/api/me')
    setCsrfToken(me.csrf_token)
    return me
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      return null
    }
    throw err
  }
}

/**
 * Membuang data milik pengguna sebelumnya. Query `me` sengaja tidak dibuang: useQuery di AuthProvider
 * terikat pada objek query itu, dan query yang dibuang (mis. lewat qc.clear()) tidak lagi
 * mengabarkan data barunya, sehingga guard tetap menganggap pengguna belum login.
 */
export function resetCacheKeepingMe(qc: QueryClient) {
  qc.removeQueries({ predicate: q => q.queryKey[0] !== keys.me[0] })
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: keys.me, queryFn: fetchMe, staleTime: 60_000, retry: false })

  // Session habis di tengah pemakaian: kosongkan cache supaya guard mengarahkan ke /login.
  useEffect(() => {
    setUnauthenticatedHandler(() => {
      qc.setQueryData(keys.me, null)
    })
    return () => setUnauthenticatedHandler(null)
  }, [qc])

  const login = useCallback(
    async (username: string, password: string) => {
      await refreshCsrfToken() // token pre-login (double-submit cookie)
      const res = await api.post<LoginResult>('/api/auth/login', { username, password })
      setCsrfToken(res.csrf_token)
      resetCacheKeepingMe(qc)
      qc.setQueryData(keys.me, await fetchMe())
    },
    [qc],
  )

  const logout = useCallback(async () => {
    try {
      await api.post('/api/auth/logout')
    } finally {
      setCsrfToken('')
      resetCacheKeepingMe(qc)
      qc.setQueryData(keys.me, null)
    }
  }, [qc])

  const value = useMemo<AuthState>(
    () => ({ me: data ?? null, isLoading, login, logout }),
    [data, isLoading, login, logout],
  )
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error('useAuth harus dipakai di dalam AuthProvider')
  }
  return ctx
}

/** Guard route: belum login → /login (dengan alamat asal). requireAdmin → halaman 403 untuk non-admin. */
export function RequireAuth({ children, requireAdmin = false }: { children: ReactNode; requireAdmin?: boolean }) {
  const { me, isLoading } = useAuth()
  const location = useLocation()
  if (isLoading) {
    return <p className="muted center-pad">Memuat…</p>
  }
  if (!me) {
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  }
  if (requireAdmin && !me.user.is_admin) {
    return <Forbidden />
  }
  return <>{children}</>
}

export function Forbidden() {
  return (
    <div className="error-page">
      <div className="error-code">403</div>
      <h1>Akses Ditolak</h1>
      <p>Anda sudah login, tetapi tidak memiliki hak akses untuk halaman ini. Hubungi Platform Admin bila Anda membutuhkan akses.</p>
    </div>
  )
}
