import { useState, type FormEvent } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { Alert, Field } from '../../components/ui'
import { useAuth } from '../../lib/auth'
import { useSubmit } from '../../lib/form'

export function Login() {
  const { me, isLoading, login } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? '/'
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const submit = useSubmit()

  if (!isLoading && me) {
    return <Navigate to={from} replace />
  }

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    const ok = await submit.run(() => login(username.trim(), password))
    if (ok) {
      navigate(from, { replace: true })
    }
  }

  return (
    <main className="login-page">
      <form className="card login-card" onSubmit={e => void onSubmit(e)} noValidate>
        <h1>Tubes WBD</h1>
        <p className="muted">Masuk untuk mengelola database dan migration.</p>
        {submit.error && <Alert>{submit.error}</Alert>}
        <Field label="Username">
          <input
            name="username"
            autoComplete="username"
            autoFocus
            required
            value={username}
            onChange={e => setUsername(e.target.value)}
          />
        </Field>
        <Field label="Password">
          <input
            name="password"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={e => setPassword(e.target.value)}
          />
        </Field>
        <button type="submit" className="btn btn-primary btn-block" disabled={submit.pending || !username || !password}>
          {submit.pending ? 'Memeriksa…' : 'Masuk'}
        </button>
      </form>
    </main>
  )
}
