import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

const outDir = resolve(__dirname, '../web/dist')

// Build mengosongkan web/dist; .gitkeep dikembalikan supaya folder tetap ada di git
// (Go membutuhkannya untuk go:embed saat frontend belum di-build).
const keepGitkeep = (): Plugin => ({
  name: 'keep-gitkeep',
  closeBundle() {
    writeFileSync(resolve(outDir, '.gitkeep'), '')
  },
})

const backend = process.env.BACKEND_URL ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react(), keepGitkeep()],
  build: { outDir, emptyOutDir: true },
  server: {
    port: 5173,
    // Saat dev, semua yang bukan milik SPA diteruskan ke server Go sehingga tidak perlu CORS.
    // /db dan /logout masih dilayani template lama milik explorer/migration sampai dipindah ke SPA.
    proxy: {
      '/api': backend,
      '/static': backend,
    },
  },
  test: { environment: 'node', include: ['src/**/*.test.ts'] },
})
