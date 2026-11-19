import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { useAuth } from '../lib/auth'

const linkClass = ({ isActive }: { isActive: boolean }) => (isActive ? 'nav-link active' : 'nav-link')

/** Kerangka halaman: header dengan navigasi yang menjadi menu hamburger di layar sempit. */
export function Layout() {
  const { me, logout } = useAuth()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const close = () => setOpen(false)

  const onLogout = async () => {
    close()
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="app">
      <a className="skip-link" href="#main">
        Lompat ke konten
      </a>
      <header className="topbar">
        <div className="topbar-inner">
          <NavLink to="/" className="brand" onClick={close}>
            <span aria-hidden="true">🌸</span> Tubes WBD
          </NavLink>
          <button
            type="button"
            className="menu-toggle"
            aria-expanded={open}
            aria-controls="primary-nav"
            onClick={() => setOpen(o => !o)}
          >
            <span className="sr-only">Menu</span>
            <span aria-hidden="true">{open ? '✕' : '☰'}</span>
          </button>
          <nav id="primary-nav" className={`nav${open ? ' open' : ''}`} aria-label="Navigasi utama">
            <NavLink to="/" end className={linkClass} onClick={close}>
              Dashboard
            </NavLink>
            {me && (
              <NavLink to="/approvals" className={linkClass} onClick={close}>
                Approval
              </NavLink>
            )}
            {me?.user.is_admin && (
              <>
                <NavLink to="/admin/users" className={linkClass} onClick={close}>
                  User
                </NavLink>
                <NavLink to="/admin/groups" className={linkClass} onClick={close}>
                  Group
                </NavLink>
                <NavLink to="/admin/databases" className={linkClass} onClick={close}>
                  Koneksi Database
                </NavLink>
              </>
            )}
            {me && (
              <div className="nav-user">
                <span className="muted" title="Pengguna saat ini">
                  {me.user.username}
                </span>
                <button type="button" className="btn btn-ghost" onClick={() => void onLogout()}>
                  Keluar
                </button>
              </div>
            )}
          </nav>
        </div>
      </header>
      <main id="main" className="content">
        <Outlet />
      </main>
    </div>
  )
}
