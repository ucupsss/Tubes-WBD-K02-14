import { Link } from 'react-router-dom'
import { api } from '../../api/client'
import { keys, useAction, usePendingApprovals } from '../../api/hooks'
import type { PendingApproval } from '../../api/types'
import { Alert, PageHeader, QueryState, TableWrap, useFlash } from '../../components/ui'
import { useAuth } from '../../lib/auth'
import { formatDateTime, useSubmit } from '../../lib/form'

/** Inbox approval: permintaan perubahan yang menunggu keputusan group yang dipimpin pengguna. */
export function Inbox() {
  const { me } = useAuth()
  const query = usePendingApprovals()
  const isLeader = (me?.led_groups.length ?? 0) > 0

  return (
    <>
      <PageHeader title="Approval" />
      {!isLeader && (
        <Alert kind="info">
          Anda bukan leader group mana pun, jadi tidak ada perubahan yang perlu Anda setujui. Leader ditetapkan oleh Platform Admin.
        </Alert>
      )}
      {isLeader && (
        <p className="muted">
          Anda memimpin: <strong>{me?.led_groups.map(g => g.name).join(', ')}</strong>. Setiap perubahan database (termasuk
          rollback dan forward) perlu disetujui leader setiap group yang memiliki akses.
        </p>
      )}
      <QueryState query={query}>
        {({ items }) =>
          items.length === 0 ? (
            <div className="card empty">
              <p>Tidak ada permintaan yang menunggu keputusan Anda.</p>
            </div>
          ) : (
            <TableWrap>
              <table>
                <thead>
                  <tr>
                    <th>Execution</th>
                    <th>Group</th>
                    <th>Diajukan</th>
                    <th>
                      <span className="sr-only">Aksi</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {items.map(item => (
                    <Row key={`${item.execution_id}-${item.group_id}`} item={item} />
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

function Row({ item }: { item: PendingApproval }) {
  const flash = useFlash()
  const submit = useSubmit()
  const action = useAction((verb: 'approve' | 'reject') => api.post(`/api/executions/${item.execution_id}/${verb}`, {}), [
    keys.approvals,
  ])

  const decide = async (verb: 'approve' | 'reject') => {
    const ok = await submit.run(() => action.mutateAsync(verb))
    if (ok) {
      flash.success(verb === 'approve' ? 'Perubahan disetujui' : 'Perubahan ditolak')
    }
  }

  return (
    <tr>
      <td data-label="Execution">
        <Link to={`/executions/${item.execution_id}`}>
          <strong>#{item.execution_id}</strong>
        </Link>
      </td>
      <td data-label="Group">{item.group_name}</td>
      <td data-label="Diajukan">{formatDateTime(item.created_at)}</td>
      <td className="row-actions">
        <button type="button" className="btn btn-primary" disabled={submit.pending} onClick={() => void decide('approve')}>
          Setujui
        </button>
        <button type="button" className="btn btn-danger-outline" disabled={submit.pending} onClick={() => void decide('reject')}>
          Tolak
        </button>
        {submit.error && <span className="field-msg">{submit.error}</span>}
      </td>
    </tr>
  )
}
