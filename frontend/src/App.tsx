import { Link, Route, Routes } from 'react-router-dom'
import { Layout } from './components/Layout'
import { RequireAuth } from './lib/auth'
import { Databases } from './pages/admin/Databases'
import { GroupDetail } from './pages/admin/GroupDetail'
import { Groups } from './pages/admin/Groups'
import { Users } from './pages/admin/Users'
import { Inbox } from './pages/approvals/Inbox'
import { Login } from './pages/auth/Login'
import { Dashboard } from './pages/dashboard/Dashboard'
import { ExecutionDetailPage } from './pages/executions/ExecutionDetail'
import { ExecutionHistoryPage } from './pages/executions/ExecutionHistory'
import { SchemaOverviewPage } from './pages/explorer/SchemaOverview'
import { TableDataPage } from './pages/explorer/TableData'
import { TableDetailPage } from './pages/explorer/TableDetail'

function NotFound() {
  return (
    <div className="error-page">
      <div className="error-code">404</div>
      <h1>Tidak Ditemukan</h1>
      <p>Halaman yang diminta tidak ada. Periksa kembali alamat URL.</p>
      <Link className="btn btn-primary" to="/">
        Kembali ke Dashboard
      </Link>
    </div>
  )
}

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<Dashboard />} />
        <Route path="approvals" element={<Inbox />} />
        <Route path="db/:dbID" element={<SchemaOverviewPage />} />
        <Route path="db/:dbID/tables/:schema/:table" element={<TableDetailPage />} />
        <Route path="db/:dbID/tables/:schema/:table/data" element={<TableDataPage />} />
        <Route path="db/:dbID/executions" element={<ExecutionHistoryPage />} />
        <Route path="executions/:execID" element={<ExecutionDetailPage />} />
        <Route
          path="admin/users"
          element={
            <RequireAuth requireAdmin>
              <Users />
            </RequireAuth>
          }
        />
        <Route
          path="admin/groups"
          element={
            <RequireAuth requireAdmin>
              <Groups />
            </RequireAuth>
          }
        />
        <Route
          path="admin/groups/:id"
          element={
            <RequireAuth requireAdmin>
              <GroupDetail />
            </RequireAuth>
          }
        />
        <Route
          path="admin/databases"
          element={
            <RequireAuth requireAdmin>
              <Databases />
            </RequireAuth>
          }
        />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  )
}
