import { useState, type FormEvent } from 'react'
import { api } from '../../api/client'
import { keys, testConnection, useAction, useConnections } from '../../api/hooks'
import type { Connection, ConnectionInput } from '../../api/types'
import { Alert, ConfirmButton, Field, PageHeader, QueryState, TableWrap, useFlash } from '../../components/ui'
import { useSubmit } from '../../lib/form'

const emptyInput: ConnectionInput = {
  name: '',
  host: '',
  port: 5432,
  db_name: '',
  username: '',
  password: '',
  sslmode: 'disable',
}

export function Databases() {
  const query = useConnections()
  const flash = useFlash()
  const [form, setForm] = useState<{ mode: 'new' } | { mode: 'edit'; conn: Connection } | null>(null)
  const del = useSubmit()
  const remove = useAction((id: number) => api.del(`/api/admin/databases/${id}`), [keys.connections])

  const onDelete = async (c: Connection) => {
    if (await del.run(() => remove.mutateAsync(c.id))) {
      flash.success(`Koneksi ${c.name} dihapus`)
    }
  }

  return (
    <>
      <PageHeader
        title="Koneksi Database"
        actions={
          <button type="button" className="btn btn-primary" onClick={() => setForm(f => (f ? null : { mode: 'new' }))}>
            {form ? 'Tutup form' : 'Tambah koneksi'}
          </button>
        }
      />
      {form && (
        <ConnectionForm
          key={form.mode === 'edit' ? form.conn.id : 'new'}
          conn={form.mode === 'edit' ? form.conn : null}
          onDone={() => setForm(null)}
        />
      )}
      {del.error && <Alert>{del.error}</Alert>}
      <QueryState query={query}>
        {({ items }) =>
          items.length === 0 ? (
            <div className="card empty">
              <p>Belum ada koneksi database target.</p>
            </div>
          ) : (
            <TableWrap>
              <table>
                <thead>
                  <tr>
                    <th>Nama</th>
                    <th>Host</th>
                    <th>Database</th>
                    <th>User</th>
                    <th>
                      <span className="sr-only">Aksi</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {items.map(c => (
                    <tr key={c.id}>
                      <td data-label="Nama">
                        <strong>{c.name}</strong>
                      </td>
                      <td data-label="Host">
                        {c.host}:{c.port}
                      </td>
                      <td data-label="Database">{c.db_name}</td>
                      <td data-label="User">{c.username}</td>
                      <td className="row-actions">
                        <button type="button" className="btn btn-ghost" onClick={() => setForm({ mode: 'edit', conn: c })}>
                          Ubah
                        </button>
                        <ConfirmButton label="Hapus" confirmLabel="Ya, hapus" onConfirm={() => void onDelete(c)} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableWrap>
          )
        }
      </QueryState>
      <p className="muted small">Koneksi yang sudah punya riwayat migration tidak dapat dihapus. Password disimpan terenkripsi dan tidak pernah ditampilkan.</p>
    </>
  )
}

function ConnectionForm({ conn, onDone }: { conn: Connection | null; onDone: () => void }) {
  const flash = useFlash()
  const submit = useSubmit()
  const test = useSubmit()
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null)
  const [v, setV] = useState<ConnectionInput>(
    conn
      ? { name: conn.name, host: conn.host, port: conn.port, db_name: conn.db_name, username: conn.username, password: '', sslmode: conn.sslmode || 'disable' }
      : emptyInput,
  )
  const set = <K extends keyof ConnectionInput>(k: K, val: ConnectionInput[K]) => setV(prev => ({ ...prev, [k]: val }))
  const save = useAction(
    (body: ConnectionInput) => (conn ? api.put(`/api/admin/databases/${conn.id}`, body) : api.post('/api/admin/databases', body)),
    [keys.connections],
  )

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (await submit.run(() => save.mutateAsync(v))) {
      flash.success(conn ? 'Koneksi diperbarui' : 'Koneksi ditambahkan')
      onDone()
    }
  }
  const onTest = async () => {
    setTestResult(null)
    await test.run(async () => setTestResult(await testConnection(conn?.id ?? null, v)))
  }

  return (
    <form className="card form-grid" onSubmit={e => void onSubmit(e)} noValidate>
      <h2>{conn ? `Ubah koneksi: ${conn.name}` : 'Koneksi baru'}</h2>
      {submit.error && Object.keys(submit.fields).length === 0 && <Alert>{submit.error}</Alert>}
      <div className="grid-2">
        <Field label="Nama koneksi" error={submit.fields.name}>
          <input value={v.name} onChange={e => set('name', e.target.value)} />
        </Field>
        <Field label="Host" error={submit.fields.host}>
          <input value={v.host} onChange={e => set('host', e.target.value)} />
        </Field>
        <Field label="Port" error={submit.fields.port}>
          <input type="number" inputMode="numeric" value={v.port} onChange={e => set('port', Number(e.target.value))} />
        </Field>
        <Field label="Nama database" error={submit.fields.db_name}>
          <input value={v.db_name} onChange={e => set('db_name', e.target.value)} />
        </Field>
        <Field label="Username" error={submit.fields.username}>
          <input value={v.username} autoComplete="off" onChange={e => set('username', e.target.value)} />
        </Field>
        <Field label="Password" error={submit.fields.password} hint={conn ? 'Kosongkan bila tidak diubah' : undefined}>
          <input type="password" value={v.password} autoComplete="new-password" onChange={e => set('password', e.target.value)} />
        </Field>
        <Field label="SSL mode">
          <select value={v.sslmode} onChange={e => set('sslmode', e.target.value)}>
            {['disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full'].map(m => (
              <option key={m}>{m}</option>
            ))}
          </select>
        </Field>
      </div>
      {test.error && <Alert>{test.error}</Alert>}
      {testResult && <Alert kind={testResult.ok ? 'success' : 'error'}>{testResult.message}</Alert>}
      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={submit.pending}>
          Simpan
        </button>
        <button type="button" className="btn btn-ghost" disabled={test.pending} onClick={() => void onTest()}>
          {test.pending ? 'Menguji…' : 'Uji koneksi'}
        </button>
      </div>
    </form>
  )
}
