import type { IncomingMessage, ServerResponse } from 'node:http'

// Verification finishes before the proxy can attach its service credential.
export async function verifyScoreSession(req: IncomingMessage, res: ServerResponse, backendUrl: string): Promise<boolean> {
  res.setHeader('Cache-Control', 'no-store')
  const authorization = req.headers.authorization
  const count = req.rawHeaders.filter((_, index) => index % 2 === 0 && req.rawHeaders[index].toLowerCase() === 'authorization').length
  if (!authorization || count !== 1) {
    res.writeHead(401, { 'Content-Type': 'application/json', 'WWW-Authenticate': 'Bearer' })
    res.end('{"error":"missing or invalid session"}')
    return false
  }
  try {
    const checked = await fetch(new URL('/auth/me', backendUrl), {
      headers: { Authorization: authorization }, redirect: 'error', signal: AbortSignal.timeout(10_000),
    })
    await checked.body?.cancel()
    if (checked.ok) return true
    const status = checked.status === 401 || checked.status === 403 ? checked.status : 503
    if (status === 401) res.setHeader('WWW-Authenticate', 'Bearer')
    res.writeHead(status, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ error: status === 503 ? 'The session could not be checked.' : 'missing or invalid session' }))
  } catch {
    res.writeHead(503, { 'Content-Type': 'application/json' })
    res.end('{"error":"The session could not be checked."}')
  }
  return false
}
