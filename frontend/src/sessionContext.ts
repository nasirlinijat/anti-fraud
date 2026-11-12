import { createContext, useContext } from 'react'
import type { Account } from './session'

export type SessionState =
  | { status: 'restoring' }
  | { status: 'unreachable'; message: string }
  | { status: 'signed-out'; notice: string | null }
  | { status: 'signed-in'; account: Account }

export interface Session {
  state: SessionState
  // signIn rejects with the request error, so the form can explain it.
  signIn: (username: string, password: string) => Promise<void>
  signOut: () => Promise<void>
  retryRestore: () => void
}

export const SessionContext = createContext<Session | null>(null)

export function useSession(): Session {
  const session = useContext(SessionContext)
  if (!session) throw new Error('Session controls require SessionProvider.')
  return session
}

// useAccount is for pages inside the console, which render only while signed in.
export function useAccount(): Account {
  const { state } = useSession()
  if (state.status !== 'signed-in') throw new Error('This page requires a signed-in account.')
  return state.account
}
