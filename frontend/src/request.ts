import { reportSessionRejected, sessionGeneration, sessionToken } from './session.ts'

export interface ErrorResponse {
  error: string
  fields?: Record<string, string>
  active_run_id?: string
}

type Failure = 'http' | 'network' | 'timeout' | 'response'

export class ApiError extends Error {
  readonly kind: Failure
  readonly status?: number
  readonly fields?: ErrorResponse['fields']
  readonly activeRunId?: string
  readonly retryAfterSeconds?: number

  constructor(kind: Failure, message: string, status?: number, details: ErrorDetails = {}) {
    super(message)
    this.kind = kind
    this.status = status
    this.fields = details.fields
    this.activeRunId = details.activeRunId
    this.retryAfterSeconds = details.retryAfterSeconds
  }
}

interface ErrorDetails {
  fields?: ErrorResponse['fields']
  activeRunId?: string
  retryAfterSeconds?: number
}

// A session request carries the signed-in account's token. A service request
// gets the service key from the console proxy. Sign-in and readiness send
// no credentials.
type Auth = 'session' | 'service' | 'none'

interface RequestOptions<T> extends Omit<RequestInit, 'signal'> {
  signal?: AbortSignal
  auth?: Auth
  expectedStatus?: number
  read?: (response: Response) => Promise<T>
}

const REQUEST_TIMEOUT_MS = 10_000

function readError(body: unknown, status: number): ErrorResponse {
  if (body && typeof body === 'object' && 'error' in body && typeof body.error === 'string') {
    const active = 'active_run_id' in body && typeof body.active_run_id === 'string' ? { active_run_id: body.active_run_id } : {}
    const fields = 'fields' in body ? body.fields : undefined
    if (fields && typeof fields === 'object' && !Array.isArray(fields) &&
      Object.values(fields).every(value => typeof value === 'string')) {
      return { error: body.error, fields: fields as Record<string, string>, ...active }
    }
    return { error: body.error, ...active }
  }
  return { error: `The backend answered with status ${status}.` }
}

function withCredentials(headers: HeadersInit | undefined, auth: Auth): Headers {
  const result = new Headers(headers)
  const token = sessionToken()
  if (auth === 'session' && token) result.set('Authorization', `Bearer ${token}`)
  return result
}

function retryAfterSeconds(response: Response): number | undefined {
  const seconds = Number(response.headers.get('Retry-After'))
  return Number.isFinite(seconds) && seconds > 0 ? seconds : undefined
}

async function httpError(response: Response, auth: Auth): Promise<ApiError> {
  const body: unknown = await response.json().catch(() => null)
  const { error, fields, active_run_id } = readError(body, response.status)
  const message = response.status === 401 && auth === 'service'
    ? `${error.endsWith('.') ? error : `${error}.`} Check API_KEY in .env and restart the console and backend.`
    : error
  return new ApiError('http', message, response.status, {
    fields, activeRunId: active_run_id, retryAfterSeconds: retryAfterSeconds(response),
  })
}

export async function request<T>(path: string, options: RequestOptions<T> = {}): Promise<T> {
  const { signal, auth = 'session', expectedStatus, read, headers, ...init } = options
  signal?.throwIfAborted()
  const controller = new AbortController()
  const cancel = () => controller.abort(signal?.reason)
  signal?.addEventListener('abort', cancel, { once: true })
  const deadline = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS)
  let response: Response | undefined
  const generation = sessionGeneration()

  try {
    response = await fetch(path, { ...init, headers: withCredentials(headers, auth), signal: controller.signal })
    if (!response.ok || (expectedStatus !== undefined && response.status !== expectedStatus)) {
      // The backend no longer accepts the token, so the console returns to sign-in.
      if (response.status === 401 && auth === 'session' && !controller.signal.aborted && generation === sessionGeneration()) reportSessionRejected()
      throw await httpError(response, auth)
    }
    const result = read ? await read(response) : await response.json() as T
    controller.signal.throwIfAborted()
    if (auth === 'session' && generation !== sessionGeneration()) throw new DOMException('Session changed.', 'AbortError')
    return result
  } catch (error) {
    signal?.throwIfAborted()
    if (error instanceof ApiError) throw error
    if (controller.signal.aborted) throw new ApiError('timeout', 'The request timed out.', response?.status)
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    if (!response) throw new ApiError('network', 'The backend cannot be reached. Is it running?')
    throw new ApiError('response', 'The backend returned an unreadable response.', response.status)
  } finally {
    clearTimeout(deadline)
    signal?.removeEventListener('abort', cancel)
  }
}

export function toErrorResponse(error: unknown): ErrorResponse {
  return {
    error: error instanceof Error ? error.message : String(error),
    ...(error instanceof ApiError && error.fields ? { fields: error.fields } : {}),
  }
}
