import { useState, type FormEvent } from 'react'
import { api } from '../../api/client'
import { keys, useAction, useUsers } from '../../api/hooks'
import type { AdminUser } from '../../api/types'
import { Alert, Badge, Field, PageHeader, QueryState, TableWrap, useFlash } from '../../components/ui'
import { useAuth } from '../../lib/auth'
import { formatDateTime, useSubmit } from '../../lib/form'

export function Users() {
  const query = useUsers()
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<number | null>(null)

  return (
    <>
      <PageHeader
        title="Kelola User"
        actions={
          <button type="button" className="btn btn-primary" onClick={() => setCreating(c => !c)}>
            {creating ? 'Tutup form' : 'Tambah user'}
          </button>
        }
      />
      {creating && <CreateUser onDone={() => setCreating(false)} />}
      <QueryState query={query}>
        {({ items }) => (
          <TableWrap>
            <table>
              <thead>
                <tr>
                  <th>Username</th>
                  <th>Peran</th>
                  <th>Status</th>
                  <th>Dibuat</th>
                  <th>
                    <span className="sr-only">Aksi</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {items.map(u => (
                  <UserRow key={u.id} user={u} isEditing={editing === u.id} onEdit={() => setEditing(editing === u.id ? null : u.id)} />
                ))}
              </tbody>
            </table>
          </TableWrap>
        )}
      </QueryState>
    </>
  )
}

function CreateUser({ onDone }: { onDone: () => void }) {
  const flash = useFlash()
  const submit = useSubmit()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [isAdmin, setIsAdmin] = useState(false)
  const create = useAction(
    (body: { username: string; password: string; is_admin: boolean }) => api.post('/api/admin/users', body),
    [keys.users],
  )

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    const ok = await submit.run(() => create.mutateAsync({ username, password, is_admin: isAdmin }))
    if (ok) {
      flash.success(`User ${username} berhasil dibuat`)
      onDone()
    }
  }

  return (
    <form className="card form-grid" onSubmit={e => void onSubmit(e)} noValidate>
      {submit.error && <Alert>{submit.error}</Alert>}
      <Field label="Username" error={submit.fields.username} hint="3–32 karakter: huruf, angka, titik, garis bawah, atau strip">
        <input value={username} onChange={e => setUsername(e.target.value)} autoComplete="off" required />
      </Field>
      <Field label="Password" error={submit.fields.password} hint="Minimal 8 karakter, maksimal 72 byte">
        <input type="password" value={password} onChange={e => setPassword(e.target.value)} autoComplete="new-password" required />
      </Field>
      <label className="check">
        <input type="checkbox" checked={isAdmin} onChange={e => setIsAdmin(e.target.checked)} />
        <span>Platform Admin</span>
      </label>
      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={submit.pending}>
          {submit.pending ? 'Menyimpan…' : 'Simpan'}
        </button>
      </div>
    </form>
  )
}

function UserRow({ user, isEditing, onEdit }: { user: AdminUser; isEditing: boolean; onEdit: () => void }) {
  const { me } = useAuth()
  const isSelf = me?.user.id === user.id
  return (
    <>
      <tr>
        <td data-label="Username">
          {user.username} {isSelf && <Badge tone="info">Anda</Badge>}
        </td>
        <td data-label="Peran">{user.is_admin ? <Badge tone="warn">Admin</Badge> : 'User'}</td>
        <td data-label="Status">{user.is_active ? <Badge tone="ok">Aktif</Badge> : <Badge tone="danger">Nonaktif</Badge>}</td>
        <td data-label="Dibuat">{formatDateTime(user.created_at)}</td>
        <td className="row-actions">
          <button type="button" className="btn btn-ghost" aria-expanded={isEditing} onClick={onEdit}>
            {isEditing ? 'Tutup' : 'Kelola'}
          </button>
        </td>
      </tr>
      {isEditing && (
        <tr className="detail-row">
          <td colSpan={5}>
            <EditUser user={user} isSelf={isSelf} />
          </td>
        </tr>
      )}
    </>
  )
}

function EditUser({ user, isSelf }: { user: AdminUser; isSelf: boolean }) {
  const flash = useFlash()
  const flags = useSubmit()
  const pw = useSubmit()
  const [isAdmin, setIsAdmin] = useState(user.is_admin)
  const [isActive, setIsActive] = useState(user.is_active)
  const [password, setPassword] = useState('')
  const update = useAction(
    (body: { is_admin: boolean; is_active: boolean }) => api.put(`/api/admin/users/${user.id}`, body),
    [keys.users],
  )
  const reset = useAction((body: { password: string }) => api.post(`/api/admin/users/${user.id}/password`, body))

  const saveFlags = async (e: FormEvent) => {
    e.preventDefault()
    if (await flags.run(() => update.mutateAsync({ is_admin: isAdmin, is_active: isActive }))) {
      flash.success('Status user diperbarui')
    }
  }
  const savePassword = async (e: FormEvent) => {
    e.preventDefault()
    if (await pw.run(() => reset.mutateAsync({ password }))) {
      setPassword('')
      flash.success('Password direset')
    }
  }

  return (
    <div className="split">
      <form onSubmit={e => void saveFlags(e)} className="form-grid">
        <h3>Status</h3>
        {flags.error && <Alert>{flags.error}</Alert>}
        <label className="check">
          <input type="checkbox" checked={isAdmin} disabled={isSelf} onChange={e => setIsAdmin(e.target.checked)} />
          <span>Platform Admin</span>
        </label>
        <label className="check">
          <input type="checkbox" checked={isActive} disabled={isSelf} onChange={e => setIsActive(e.target.checked)} />
          <span>Akun aktif</span>
        </label>
        {isSelf && <p className="muted">Anda tidak bisa mencabut status admin atau menonaktifkan akun sendiri.</p>}
        <div className="form-actions">
          <button type="submit" className="btn btn-primary" disabled={flags.pending || isSelf}>
            Simpan status
          </button>
        </div>
      </form>
      <form onSubmit={e => void savePassword(e)} className="form-grid" noValidate>
        <h3>Reset password</h3>
        {pw.error && !pw.fields.password && <Alert>{pw.error}</Alert>}
        <Field label="Password baru" error={pw.fields.password}>
          <input type="password" value={password} autoComplete="new-password" onChange={e => setPassword(e.target.value)} />
        </Field>
        <div className="form-actions">
          <button type="submit" className="btn btn-primary" disabled={pw.pending || !password}>
            Reset password
          </button>
        </div>
      </form>
    </div>
  )
}
