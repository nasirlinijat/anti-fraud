// Calls for sign-in, accounts, and reviewer assignments described in openapi.yaml.

import { save, type Saved } from './api.ts'
import { request } from './request.ts'
import type { Account, Role } from './session.ts'

export interface SignIn {
  token: string
  expires_at: string
  account: Account
}

// A sign-in request has no session yet, so a 401 here means wrong credentials.
export function signIn(username: string, password: string): Promise<SignIn> {
  return request('/auth/login', {
    method: 'POST',
    auth: 'none',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
}

export function getSignedInAccount(signal?: AbortSignal): Promise<Account> {
  return request('/auth/me', { signal })
}

export function signOut(): Promise<void> {
  return request('/auth/logout', { method: 'POST', read: async () => {} })
}

export interface AccountDetails extends Account {
  disabled: boolean
  created_at: string
}

export interface NewAccount {
  username: string
  password: string
  role: Role
}

export async function listAccounts(signal?: AbortSignal): Promise<AccountDetails[]> {
  const result = await request<{ accounts: AccountDetails[] }>('/admin/accounts', { signal })
  return result.accounts
}

export function createAccount(account: NewAccount): Promise<Saved<AccountDetails>> {
  return save(() => request<AccountDetails>('/admin/accounts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(account),
  }))
}

export function disableAccount(username: string): Promise<Saved<AccountDetails>> {
  return save(() => request<AccountDetails>(`/admin/accounts/${encodeURIComponent(username)}/disable`, { method: 'POST' }))
}

export interface OpenClaim {
  purchase_id: string
  amount_minor: string
  rule_score: number
  model_score: number | null
  claimed_at: string
}

export interface ReviewerWork extends Account {
  disabled: boolean
  decided: number
  open_claims: OpenClaim[]
}

export async function listAssignments(signal?: AbortSignal): Promise<ReviewerWork[]> {
  const result = await request<{ reviewers: ReviewerWork[] }>('/admin/assignments', { signal })
  return result.reviewers
}
