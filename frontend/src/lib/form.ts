import { useCallback, useState } from 'react'
import { ApiError, type FieldErrors } from '../api/client'

export interface SubmitState {
  pending: boolean
  error: string
  fields: FieldErrors
  /** Menjalankan aksi; mengembalikan true bila berhasil. Error API masuk ke state, bukan dilempar. */
  run: (fn: () => Promise<unknown>) => Promise<boolean>
  reset: () => void
}

/** State bersama untuk form/aksi: pending, pesan error umum, dan error per field dari API. */
export function useSubmit(): SubmitState {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const [fields, setFields] = useState<FieldErrors>({})

  const reset = useCallback(() => {
    setError('')
    setFields({})
  }, [])

  const run = useCallback(async (fn: () => Promise<unknown>) => {
    setPending(true)
    setError('')
    setFields({})
    try {
      await fn()
      return true
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message)
        setFields(err.fields)
      } else {
        setError('Terjadi kesalahan tak terduga')
      }
      return false
    } finally {
      setPending(false)
    }
  }, [])

  return { pending, error, fields, run, reset }
}

/** Format waktu ISO ke tanggal-jam lokal Indonesia. */
export function formatDateTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) {
    return iso
  }
  return d.toLocaleString('id-ID', { dateStyle: 'medium', timeStyle: 'short' })
}
