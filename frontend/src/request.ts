export interface ErrorResponse {
  error: string
  fields?: Record<string, string>
}

type Failure = 'http' | 'network' | 'timeout' | 'response'

export class ApiError extends Error {
  readonly kind: Failure
  readonly status?: number
  readonly fields?: ErrorResponse['fields']

  constructor(kind: Failure, message: string, status?: number, fields?: ErrorResponse['fields']) {
    super(message)
    this.kind = kind
    this.status = status
    this.fields = fields
  }
}

interface RequestOptions<T> extends Omit<RequestInit, 'signal'> {
  signal?: AbortSignal
  auth?: 'service' | 'analyst' | 'operator' | 'none'
  expectedStatus?: number
  read?: (response: Response) => Promise<T>
}

const REQUEST_TIMEOUT_MS = 10_000

function readError(body: unknown, status: number): ErrorResponse {
  if (body && typeof body === 'object' && 'error' in body && typeof body.error === 'string') {
    const fields = 'fields' in body ? body.fields : undefined
    if (fields && typeof fields === 'object' && !Array.isArray(fields) &&
      Object.values(fields).every(value => typeof value === 'string')) {
      return { error: body.error, fields: fields as Record<string, string> }
    }
    return { error: body.error }
  }
  return { error: `The backend answered with status ${status}.` }
}

export async function request<T>(path: string, options: RequestOptions<T> = {}): Promise<T> {
  const { signal, auth = 'service', expectedStatus, read, ...init } = options
  signal?.throwIfAborted()
  const controller = new AbortController()
  const cancel = () => controller.abort(signal?.reason)
  signal?.addEventListener('abort', cancel, { once: true })
  const deadline = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS)
  let response: Response | undefined

  try {
    response = await fetch(path, { ...init, signal: controller.signal })
    if (!response.ok || (expectedStatus !== undefined && response.status !== expectedStatus)) {
      const body: unknown = await response.json().catch(() => null)
      const { error, fields } = readError(body, response.status)
      const message = response.status === 401 && auth === 'service'
        ? `${error.endsWith('.') ? error : `${error}.`} Check API_KEY in .env and restart the console and backend.`
        : error
      throw new ApiError('http', message, response.status, fields)
    }
    const result = read ? await read(response) : await response.json() as T
    controller.signal.throwIfAborted()
    return result
  } catch (error) {
    signal?.throwIfAborted()
    if (error instanceof ApiError) throw error
    if (controller.signal.aborted) throw new ApiError('timeout', 'The request timed out.', response?.status)
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
