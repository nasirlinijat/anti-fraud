import path from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // The repository .env holds PORT and API_KEY for the backend; frontend/.env and
  // the shell can override them.
  const env = {
    ...loadEnv(mode, path.resolve(import.meta.dirname, '..'), ''),
    ...loadEnv(mode, process.cwd(), ''),
  }
  const backendUrl = env.BACKEND_URL || `http://localhost:${env.PORT || '8080'}`

  return {
    plugins: [react()],
    server: {
      // Forward backend requests so the browser calls the same origin and needs no CORS.
      proxy: {
        '/health': backendUrl,
        '/ready': backendUrl,
        // The tester uses the API key from the dev server.
        '/score': { target: backendUrl, headers: { 'X-API-Key': env.API_KEY ?? '' } },
        '/stats': { target: backendUrl, headers: { 'X-API-Key': env.API_KEY ?? '' } },
        '/blocklist': { target: backendUrl, headers: { 'X-API-Key': env.API_KEY ?? '' } },
        // Forward the key entered in the review queue.
        '/reviews': backendUrl,
      },
    },
  }
})
