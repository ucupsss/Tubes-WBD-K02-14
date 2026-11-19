import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { ApiError } from './api/client'
import { App } from './App'
import { FlashProvider } from './components/ui'
import { AuthProvider } from './lib/auth'
import './styles.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Jangan mengulang request yang ditolak server (4xx); ulangi sekali untuk gangguan jaringan.
      retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 1,
      refetchOnWindowFocus: false,
    },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <FlashProvider>
          <AuthProvider>
            <App />
          </AuthProvider>
        </FlashProvider>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
