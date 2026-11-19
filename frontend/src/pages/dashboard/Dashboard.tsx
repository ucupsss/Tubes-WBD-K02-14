import { useAccessibleDatabases } from '../../api/hooks'
import { Badge, PageHeader, QueryState, TableWrap } from '../../components/ui'
import { useAuth } from '../../lib/auth'

/**
 * Dashboard: database yang boleh diakses pengguna.
 * Halaman explorer dan migration masih dilayani server (template lama) sampai dipindah ke SPA,
 * sehingga tautan di sini memakai navigasi biasa (<a>), bukan router SPA.
 */
export function Dashboard() {
  const { me } = useAuth()
  const query = useAccessibleDatabases()

  return (
    <>
      <PageHeader title="Dashboard" />
      <p className="muted">
        Halo, <strong>{me?.user.username}</strong>. Berikut database yang dapat Anda akses.
      </p>
      <QueryState query={query}>
        {({ items }) =>
          items.length === 0 ? (
            <div className="card empty">
              <p>Anda belum memiliki akses ke database mana pun.</p>
              <p className="muted">Minta Platform Admin menambahkan Anda ke group yang memiliki akses.</p>
            </div>
          ) : (
            <TableWrap>
              <table>
                <thead>
                  <tr>
                    <th>Database</th>
                    <th>Akses</th>
                    <th>
                      <span className="sr-only">Aksi</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {items.map(db => (
                    <tr key={db.id}>
                      <td data-label="Database">{db.name}</td>
                      <td data-label="Akses">
                        <Badge tone={db.level === 'edit' ? 'ok' : 'info'}>{db.level === 'edit' ? 'view + edit' : 'view'}</Badge>
                      </td>
                      <td className="row-actions">
                        <a className="btn btn-ghost" href={`/db/${db.id}`}>
                          Schema
                        </a>
                        <a className="btn btn-ghost" href={`/db/${db.id}/migrations`}>
                          Migration
                        </a>
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
