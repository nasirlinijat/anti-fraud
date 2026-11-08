import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const backendUrl = env.BACKEND_URL || 'http://localhost:8080'

  return {
    plugins: [react()],
    server: {
      // Forward backend requests so the browser calls the same origin and needs no CORS.
      proxy: {
        '/health': backendUrl,
        '/ready': backendUrl,
      },
    },
  }
})
