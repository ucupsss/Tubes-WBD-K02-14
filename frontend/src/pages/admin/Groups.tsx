import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../../api/client'
import { keys, useAction, useGroups } from '../../api/hooks'
import { Alert, Badge, Field, PageHeader, QueryState, TableWrap, useFlash } from '../../components/ui'
import { useSubmit } from '../../lib/form'

export function Groups() {
  const query = useGroups()
  const [creating, setCreating] = useState(false)

  return (
    <>
      <PageHeader
        title="Kelola Group"
        actions={
          <button type="button" className="btn btn-primary" onClick={() => setCreating(c => !c)}>
            {creating ? 'Tutup form' : 'Tambah group'}
          </button>
        }
      />
      {creating && <CreateGroup onDone={() => setCreating(false)} />}
      <QueryState query={query}>
        {({ items }) =>
          items.length === 0 ? (
            <div className="card empty">
              <p>Belum ada group.</p>
            </div>
          ) : (
            <TableWrap>
              <table>
                <thead>
                  <tr>
                    <th>Nama</th>
                    <th>Leader</th>
                    <th>Anggota</th>
                    <th>
                      <span className="sr-only">Aksi</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {items.map(g => (
                    <tr key={g.id}>
                      <td data-label="Nama">
                        <strong>{g.name}</strong>
                        {g.description && <div className="muted small">{g.description}</div>}
                      </td>
                      <td data-label="Leader">{g.leader_id ? g.leader_name : <Badge tone="warn">Belum ada</Badge>}</td>
                      <td data-label="Anggota">{g.member_count}</td>
                      <td className="row-actions">
                        <Link className="btn btn-ghost" to={`/admin/groups/${g.id}`}>
                          Kelola
                        </Link>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </TableWrap>
          )
        }
      </QueryState>
    </>
  )
}

function CreateGroup({ onDone }: { onDone: () => void }) {
  const flash = useFlash()
  const submit = useSubmit()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const create = useAction((body: { name: string; description: string }) => api.post('/api/admin/groups', body), [keys.groups])

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (await submit.run(() => create.mutateAsync({ name, description }))) {
      flash.success(`Group ${name} dibuat`)
      onDone()
    }
  }

  return (
    <form className="card form-grid" onSubmit={e => void onSubmit(e)} noValidate>
      {submit.error && Object.keys(submit.fields).length === 0 && <Alert>{submit.error}</Alert>}
      <Field label="Nama group" error={submit.fields.name}>
        <input value={name} maxLength={64} onChange={e => setName(e.target.value)} required />
      </Field>
      <Field label="Deskripsi" error={submit.fields.description}>
        <textarea value={description} rows={2} maxLength={500} onChange={e => setDescription(e.target.value)} />
      </Field>
      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={submit.pending}>
          Simpan
        </button>
      </div>
    </form>
  )
}
