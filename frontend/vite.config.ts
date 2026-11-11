import path from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

const serviceRoutes = '^/(?:score|stats|blocklist|rulesets)(?:/|\\?|$)'
const reviewRoutes = '^/reviews(?:/|\\?|$)'
const protectedRoute = new RegExp(`${serviceRoutes}|${reviewRoutes}`)
const loopbackHost = /^(?:localhost|127\.0\.0\.1|\[::1\])(?::\d+)?$/i

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
    plugins: [react(), {
      name: 'local-console-api',
      configureServer(server) {
        server.middlewares.use((req, res, next) => {
          if (!protectedRoute.test(req.url ?? '')) {
            next()
            return
          }
          const { host, origin, 'sec-fetch-site': site } = req.headers
          if (host && loopbackHost.test(host) && (origin === undefined || origin === `http://${host}`) &&
              site !== 'cross-site' && site !== 'same-site') {
            next()
            return
          }
          res.writeHead(403, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
          res.end('{"error":"the local console API requires a same-origin request"}')
        })
      },
    }],
    server: {
      // Forward backend requests so the browser calls the same origin and needs no CORS.
      proxy: {
        '/health': backendUrl,
        '/ready': backendUrl,
        [serviceRoutes]: { target: backendUrl, headers: { 'X-API-Key': env.API_KEY ?? '' } },
        // Forward the key entered in the review queue.
        [reviewRoutes]: backendUrl,
      },
    },
  }
})
