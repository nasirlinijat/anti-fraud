// The session token and the listeners told when the backend stops accepting it.
// This file has no React or DOM imports, so the request layer and its Node
// tests can use it.

export type Role = 'reviewer' | 'admin'

export interface Account {
  username: string
  role: Role
}

export const sessionStorageKey = 'anti-fraud.session'

let token: string | null = null
const rejectionListeners = new Set<() => void>()

function browserStorage(): Storage | null {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage
  } catch {
    // A blocked store keeps the session in memory for this page only.
    return null
  }
}

export function sessionToken(): string | null {
  return token
}

// restoreSessionToken reads the token saved by an earlier page load or by
// another tab, and makes it the current one.
export function restoreSessionToken(): string | null {
  try {
    token = browserStorage()?.getItem(sessionStorageKey) || null
  } catch {
    // An unreadable store behaves like a first visit.
    token = null
  }
  return token
}

export function keepSessionToken(value: string) {
  token = value
  try {
    browserStorage()?.setItem(sessionStorageKey, value)
  } catch {
    // The session still works in memory; it ends when the page is closed.
  }
}

export function forgetSessionToken() {
  token = null
  try {
    browserStorage()?.removeItem(sessionStorageKey)
  } catch {
    // Nothing was saved, so there is nothing to remove.
  }
}

export function onSessionRejected(listener: () => void): () => void {
  rejectionListeners.add(listener)
  return () => rejectionListeners.delete(listener)
}

export function reportSessionRejected() {
  for (const listener of rejectionListeners) listener()
}
