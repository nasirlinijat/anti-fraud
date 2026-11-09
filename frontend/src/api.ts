// Types and calls for the score API described in openapi.yaml.

export type Outcome = 'allow' | 'review' | 'block'

export interface FiredRule {
  name: string
  points: number
}

export interface ScoreResponse {
  transaction_id: string
  outcome: Outcome
  score: number
  fired_rules: FiredRule[]
  ruleset_version: number
  model_score: number | null
}

export interface ErrorResponse {
  error: string
  fields?: Record<string, string>
}

export type ScoreResult =
  | { ok: true; response: ScoreResponse }
  | { ok: false; status: number; error: ErrorResponse }

// score sends one score request. The development server adds the API key.
export async function score(request: Record<string, unknown>): Promise<ScoreResult> {
  let res: Response
  try {
    res = await fetch('/score', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(request),
    })
  } catch {
    return { ok: false, status: 0, error: { error: 'The backend cannot be reached. Is it running?' } }
  }

  const body: unknown = await res.json().catch(() => null)
  if (res.ok) {
    return { ok: true, response: body as ScoreResponse }
  }
  const error =
    body && typeof body === 'object' && 'error' in body
      ? (body as ErrorResponse)
      : { error: `The backend answered with status ${res.status}.` }
  if (res.status === 401) {
    error.error += '. Set API_KEY in the .env file and restart npm run dev.'
  }
  return { ok: false, status: res.status, error }
}
