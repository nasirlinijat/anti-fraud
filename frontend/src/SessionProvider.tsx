import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { getSignedInAccount, signIn as requestSignIn, signOut as requestSignOut } from './accountApi'
import { ApiError } from './request'
import {
  forgetSessionToken, keepSessionToken, onSessionRejected, restoreSessionToken, sessionStorageKey, sessionGeneration,
} from './session'
import { SessionContext, type SessionState } from './sessionContext'
import { usePolling } from './usePolling'

const endedNotice = 'Your session has ended. Sign in again to continue.'

function initialState(): SessionState {
  return restoreSessionToken() ? { status: 'restoring' } : { status: 'signed-out', notice: null }
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SessionState>(initialState)

  const { refresh } = usePolling(state.status === 'restoring' || state.status === 'signed-in' ? getSignedInAccount : null, {
    intervalMs: 60_000,
    waitAfterCompletion: true,
    onSuccess: (account) => setState({ status: 'signed-in', account }),
    onError: (error) => {
      if (state.status === 'signed-in' || (error instanceof ApiError && error.status === 401)) return
      setState({ status: 'unreachable', message: error instanceof Error ? error.message : 'The session could not be checked.' })
    },
  })

  useEffect(() => onSessionRejected(() => {
    forgetSessionToken()
    setState({ status: 'signed-out', notice: endedNotice })
  }), [])

  useSessionFromOtherTabs(setState, refresh)
  useSessionVisibility(refresh)

  const signIn = useCallback(async (username: string, password: string) => {
    const generation = sessionGeneration()
    const session = await requestSignIn(username, password)
    if (generation !== sessionGeneration()) return
    keepSessionToken(session.token)
    setState({ status: 'signed-in', account: session.account })
  }, [])

  const signOut = useCallback(async () => {
    const generation = sessionGeneration()
    let notice = 'You have signed out.'
    try {
      await requestSignOut()
    } catch (error) {
      // The token is forgotten here either way; the saved session then ends when it expires.
      if (error instanceof ApiError && error.status === 401) notice = endedNotice
      else notice = 'You are signed out on this device. The backend could not be reached, so the session ends when it expires.'
    }
    if (generation !== sessionGeneration()) return
    forgetSessionToken()
    setState({ status: 'signed-out', notice })
  }, [])

  const retryRestore = useCallback(() => setState({ status: 'restoring' }), [])

  return <SessionContext.Provider value={{ state, signIn, signOut, retryRestore }}>{children}</SessionContext.Provider>
}

// Signing in or out in one tab applies to every tab of this browser.
function useSessionFromOtherTabs(setState: (state: SessionState) => void, refresh: () => void) {
  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key !== sessionStorageKey && event.key !== null) return
      setState(restoreSessionToken()
        ? { status: 'restoring' }
        : { status: 'signed-out', notice: 'You signed out in another tab.' })
      refresh()
    }
    window.addEventListener('storage', sync)
    return () => window.removeEventListener('storage', sync)
  }, [setState, refresh])
}

function useSessionVisibility(refresh: () => void) {
  useEffect(() => {
    const check = () => { if (document.visibilityState === 'visible') refresh() }
    window.addEventListener('focus', check)
    document.addEventListener('visibilitychange', check)
    return () => {
      window.removeEventListener('focus', check)
      document.removeEventListener('visibilitychange', check)
    }
  }, [refresh])
}
