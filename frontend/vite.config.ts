import path from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

const scoreRoute = '^/score(?:/|\\?|$)'
const sessionRoutes = '^/(?:auth|admin|reviews|purchases|model|stats|blocklist|rulesets|simulations)(?:/|\\?|$)'
const protectedRoute = new RegExp(`${scoreRoute}|${sessionRoutes}`)
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
        // POST /score accepts only the service key, so the proxy adds it for the Tester page.
        [scoreRoute]: {
          target: backendUrl,
          configure(proxy) {
            proxy.on('proxyReq', outgoing => outgoing.setHeader('X-API-Key', env.API_KEY ?? ''))
          },
        },
        // Every other console request carries the session token of the signed-in account.
        [sessionRoutes]: backendUrl,
      },
    },
  }
})
