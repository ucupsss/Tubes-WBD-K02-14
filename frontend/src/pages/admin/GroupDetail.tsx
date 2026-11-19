import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api } from '../../api/client'
import { keys, useAction, useGroup, useGroupAccess } from '../../api/hooks'
import type { AccessLevel, GroupDetail as Detail } from '../../api/types'
import { Alert, Badge, ConfirmButton, Field, PageHeader, QueryState, TableWrap, useFlash } from '../../components/ui'
import { useSubmit } from '../../lib/form'

export function GroupDetail() {
  const id = Number(useParams().id)
  const query = useGroup(id)
  return (
    <>
      <p>
        <Link to="/admin/groups">← Semua group</Link>
      </p>
      <QueryState query={query}>
        {detail => (
          <>
            <PageHeader title={detail.group.name} />
            <Info detail={detail} />
            <Leader detail={detail} />
            <Members detail={detail} />
            <Access groupId={id} />
          </>
        )}
      </QueryState>
    </>
  )
}

function Info({ detail }: { detail: Detail }) {
  const { group } = detail
  const flash = useFlash()
  const navigate = useNavigate()
  const submit = useSubmit()
  const del = useSubmit()
  const [name, setName] = useState(group.name)
  const [description, setDescription] = useState(group.description)
  const update = useAction((b: { name: string; description: string }) => api.put(`/api/admin/groups/${group.id}`, b), [
    keys.group(group.id),
    keys.groups,
  ])
  const remove = useAction(() => api.del(`/api/admin/groups/${group.id}`), [keys.groups])

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (await submit.run(() => update.mutateAsync({ name, description }))) {
      flash.success('Group diperbarui')
    }
  }
  const onDelete = async () => {
    if (await del.run(() => remove.mutateAsync(undefined))) {
      flash.success('Group dihapus')
      navigate('/admin/groups')
    }
  }

  return (
    <section className="card">
      <h2>Informasi</h2>
      <form className="form-grid" onSubmit={e => void onSubmit(e)} noValidate>
        {submit.error && Object.keys(submit.fields).length === 0 && <Alert>{submit.error}</Alert>}
        <Field label="Nama group" error={submit.fields.name}>
          <input value={name} maxLength={64} onChange={e => setName(e.target.value)} />
        </Field>
        <Field label="Deskripsi" error={submit.fields.description}>
          <textarea rows={2} value={description} maxLength={500} onChange={e => setDescription(e.target.value)} />
        </Field>
        <div className="form-actions">
          <button type="submit" className="btn btn-primary" disabled={submit.pending}>
            Simpan
          </button>
          <ConfirmButton label="Hapus group" confirmLabel="Ya, hapus group" onConfirm={() => void onDelete()} disabled={del.pending} />
        </div>
        {del.error && <Alert>{del.error}</Alert>}
        <p className="muted small">Menghapus group juga menghapus keanggotaan dan akses database group ini.</p>
      </form>
    </section>
  )
}

function Leader({ detail }: { detail: Detail }) {
  const { group, members } = detail
  const flash = useFlash()
  const submit = useSubmit()
  const [userId, setUserId] = useState<string>(group.leader_id ? String(group.leader_id) : '')
  useEffect(() => setUserId(group.leader_id ? String(group.leader_id) : ''), [group.leader_id])
  const setLeader = useAction((id: number | null) => api.put(`/api/admin/groups/${group.id}/leader`, { user_id: id }), [
    keys.group(group.id),
    keys.groups,
  ])

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (await submit.run(() => setLeader.mutateAsync(userId === '' ? null : Number(userId)))) {
      flash.success('Leader diperbarui')
    }
  }

  return (
    <section className="card">
      <h2>Leader</h2>
      <p className="muted">
        Leader menyetujui perubahan database atas nama group ini. Selama belum ada leader, pengajuan perubahan tetap menunggu
        (tidak ditolak).
      </p>
      <form className="form-row" onSubmit={e => void onSubmit(e)}>
        {submit.error && <Alert>{submit.error}</Alert>}
        <Field label="Leader group">
          <select value={userId} onChange={e => setUserId(e.target.value)}>
            <option value="">— Belum ada leader —</option>
            {members
              .filter(m => m.is_active)
              .map(m => (
                <option key={m.user_id} value={m.user_id}>
                  {m.username}
                </option>
              ))}
          </select>
        </Field>
        <button type="submit" className="btn btn-primary" disabled={submit.pending || members.length === 0}>
          Simpan leader
        </button>
      </form>
      {members.length === 0 && <p className="muted small">Tambahkan anggota terlebih dahulu; leader harus anggota group.</p>}
    </section>
  )
}

function Members({ detail }: { detail: Detail }) {
  const { group, members, candidates } = detail
  const flash = useFlash()
  const add = useSubmit()
  const remove = useSubmit()
  const [userId, setUserId] = useState('')
  const invalidate = [keys.group(group.id), keys.groups]
  const addMember = useAction((id: number) => api.post(`/api/admin/groups/${group.id}/members`, { user_id: id }), invalidate)
  const removeMember = useAction((id: number) => api.del(`/api/admin/groups/${group.id}/members/${id}`), invalidate)

  const onAdd = async (e: FormEvent) => {
    e.preventDefault()
    if (userId && (await add.run(() => addMember.mutateAsync(Number(userId))))) {
      setUserId('')
      flash.success('Anggota ditambahkan')
    }
  }
  const onRemove = async (id: number) => {
    if (await remove.run(() => removeMember.mutateAsync(id))) {
      flash.success('Anggota dikeluarkan')
    }
  }

  return (
    <section className="card">
      <h2>Anggota ({members.length})</h2>
      {remove.error && <Alert>{remove.error}</Alert>}
      {members.length === 0 ? (
        <p className="muted">Belum ada anggota.</p>
      ) : (
        <TableWrap>
          <table>
            <thead>
              <tr>
                <th>Username</th>
                <th>Status</th>
                <th>
                  <span className="sr-only">Aksi</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {members.map(m => (
                <tr key={m.user_id}>
                  <td data-label="Username">
                    {m.username} {m.is_leader && <Badge tone="info">Leader</Badge>}
                  </td>
                  <td data-label="Status">{m.is_active ? <Badge tone="ok">Aktif</Badge> : <Badge tone="danger">Nonaktif</Badge>}</td>
                  <td className="row-actions">
                    <ConfirmButton label="Keluarkan" confirmLabel="Ya, keluarkan" onConfirm={() => void onRemove(m.user_id)} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableWrap>
      )}
      <form className="form-row" onSubmit={e => void onAdd(e)}>
        {add.error && <Alert>{add.error}</Alert>}
        <Field label="Tambah anggota">
          <select value={userId} onChange={e => setUserId(e.target.value)}>
            <option value="">— Pilih user —</option>
            {candidates.map(c => (
              <option key={c.id} value={c.id}>
                {c.username}
              </option>
            ))}
          </select>
        </Field>
        <button type="submit" className="btn btn-primary" disabled={add.pending || !userId}>
          Tambah
        </button>
      </form>
    </section>
  )
}

function Access({ groupId }: { groupId: number }) {
  const query = useGroupAccess(groupId)
  return (
    <section className="card">
      <h2>Akses database</h2>
      <p className="muted">
        Group dengan akses <em>view</em> maupun <em>edit</em> ke sebuah database wajib menyetujui setiap perubahan pada database itu.
      </p>
      <QueryState query={query}>
        {({ items }) => (items.length === 0 ? <p className="muted">Belum ada koneksi database.</p> : <AccessForm groupId={groupId} initial={items} />)}
      </QueryState>
    </section>
  )
}

function AccessForm({ groupId, initial }: { groupId: number; initial: { database_id: number; database_name: string; level: AccessLevel }[] }) {
  const flash = useFlash()
  const submit = useSubmit()
  const [levels, setLevels] = useState<Record<number, AccessLevel>>(() =>
    Object.fromEntries(initial.map(g => [g.database_id, g.level])),
  )
  const save = useAction(
    (grants: Record<string, AccessLevel>) => api.put(`/api/admin/groups/${groupId}/access`, { grants }),
    [keys.access(groupId)],
  )

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    const grants = Object.fromEntries(Object.entries(levels).map(([id, level]) => [id, level]))
    if (await submit.run(() => save.mutateAsync(grants))) {
      flash.success('Akses database disimpan')
    }
  }

  return (
    <form onSubmit={e => void onSubmit(e)} className="form-grid">
      {submit.error && <Alert>{submit.error}</Alert>}
      <TableWrap>
        <table>
          <thead>
            <tr>
              <th>Database</th>
              <th>Level</th>
            </tr>
          </thead>
          <tbody>
            {initial.map(g => (
              <tr key={g.database_id}>
                <td data-label="Database">{g.database_name}</td>
                <td data-label="Level">
                  <select
                    aria-label={`Level akses ${g.database_name}`}
                    value={levels[g.database_id]}
                    onChange={e => setLevels(l => ({ ...l, [g.database_id]: e.target.value as AccessLevel }))}
                  >
                    <option value="none">Tidak ada</option>
                    <option value="view">View</option>
                    <option value="edit">View + Edit</option>
                  </select>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableWrap>
      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={submit.pending}>
          Simpan akses
        </button>
      </div>
    </form>
  )
}
