import type { UseQueryResult } from '@tanstack/react-query'
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ApiError } from '../api/client'

// ---- Notifikasi singkat (flash) ----

interface FlashState {
  message: string
  kind: 'success' | 'error'
}
interface FlashApi {
  success: (message: string) => void
  error: (message: string) => void
}
const FlashContext = createContext<FlashApi | null>(null)

export function FlashProvider({ children }: { children: ReactNode }) {
  const [flash, setFlash] = useState<FlashState | null>(null)
  useEffect(() => {
    if (!flash) {
      return
    }
    const t = window.setTimeout(() => setFlash(null), 5000)
    return () => window.clearTimeout(t)
  }, [flash])

  const success = useCallback((message: string) => setFlash({ message, kind: 'success' }), [])
  const error = useCallback((message: string) => setFlash({ message, kind: 'error' }), [])
  const api = useMemo(() => ({ success, error }), [success, error])

  return (
    <FlashContext.Provider value={api}>
      {children}
      {flash && (
        <div className={`flash flash-${flash.kind}`} role="status" aria-live="polite">
          <span>{flash.message}</span>
          <button type="button" className="icon-btn" aria-label="Tutup pesan" onClick={() => setFlash(null)}>
            ×
          </button>
        </div>
      )}
    </FlashContext.Provider>
  )
}

export function useFlash(): FlashApi {
  const ctx = useContext(FlashContext)
  if (!ctx) {
    throw new Error('useFlash harus dipakai di dalam FlashProvider')
  }
  return ctx
}

// ---- Elemen dasar ----

export function Alert({ children, kind = 'error' }: { children: ReactNode; kind?: 'error' | 'info' | 'success' }) {
  return (
    <div className={`alert alert-${kind}`} role={kind === 'error' ? 'alert' : 'status'}>
      {children}
    </div>
  )
}

export function Badge({ children, tone = 'neutral' }: { children: ReactNode; tone?: 'neutral' | 'ok' | 'warn' | 'danger' | 'info' }) {
  return <span className={`badge badge-${tone}`}>{children}</span>
}

export function PageHeader({ title, actions }: { title: string; actions?: ReactNode }) {
  return (
    <div className="page-header">
      <h1>{title}</h1>
      {actions && <div className="page-actions">{actions}</div>}
    </div>
  )
}

export function Field({
  label,
  error,
  hint,
  children,
}: {
  label: string
  error?: string
  hint?: string
  children: ReactNode
}) {
  return (
    <label className={`field${error ? ' field-error' : ''}`}>
      <span className="field-label">{label}</span>
      {children}
      {hint && !error && <span className="field-hint">{hint}</span>}
      {error && <span className="field-msg">{error}</span>}
    </label>
  )
}

/** Pembungkus tabel lebar: scroll horizontal terjadi di dalam komponen, bukan di halaman. */
export function TableWrap({ children }: { children: ReactNode }) {
  return <div className="table-wrap">{children}</div>
}

/** Menampilkan status loading/error dari useQuery, lalu isi saat data siap. */
export function QueryState<T>({ query, children }: { query: UseQueryResult<T>; children: (data: T) => ReactNode }) {
  if (query.isPending) {
    return <p className="muted" aria-busy="true">Memuat…</p>
  }
  if (query.isError) {
    const msg = query.error instanceof ApiError ? query.error.message : 'Gagal memuat data'
    return (
      <Alert>
        {msg}{' '}
        <button type="button" className="link-btn" onClick={() => void query.refetch()}>
          Coba lagi
        </button>
      </Alert>
    )
  }
  return <>{children(query.data)}</>
}

/** Tombol dengan konfirmasi dua langkah untuk aksi berbahaya (tanpa window.confirm). */
export function ConfirmButton({
  label,
  confirmLabel = 'Ya, lanjutkan',
  onConfirm,
  disabled,
}: {
  label: string
  confirmLabel?: string
  onConfirm: () => void
  disabled?: boolean
}) {
  const [asking, setAsking] = useState(false)
  if (!asking) {
    return (
      <button type="button" className="btn btn-danger-outline" disabled={disabled} onClick={() => setAsking(true)}>
        {label}
      </button>
    )
  }
  return (
    <span className="confirm">
      <button
        type="button"
        className="btn btn-danger"
        onClick={() => {
          setAsking(false)
          onConfirm()
        }}
      >
        {confirmLabel}
      </button>
      <button type="button" className="btn btn-ghost" onClick={() => setAsking(false)}>
        Batal
      </button>
    </span>
  )
}
