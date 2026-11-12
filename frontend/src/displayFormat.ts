import type { AccountEventType, Outcome } from './api.ts'

const usd = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' })
const relativeTime = new Intl.RelativeTimeFormat('en-US', { numeric: 'always', style: 'short' })

export const outcomeLabels: Record<Outcome, string> = {
  allow: 'Allowed',
  review: 'Sent to review',
  block: 'Blocked',
}

export const accountEventLabels: Record<AccountEventType, string> = {
  login: 'Login',
  password_reset: 'Password reset',
}

export function formatUsdMinor(value: string | null | undefined): string {
  if (value == null || !/^[0-9]+$/.test(value)) return 'Unavailable'
  const minor = BigInt(value)
  const cents = (minor % 100n).toString().padStart(2, '0')
  return usd.formatToParts(minor / 100n)
    .map(part => part.type === 'fraction' ? cents : part.value)
    .join('')
}

export function formatTimestamp(value: string, now: number) {
  const time = Date.parse(value)
  if (!Number.isFinite(time)) return null

  const iso = new Date(time).toISOString()
  const utc = iso.replace('T', ' ').replace('Z', ' UTC')
  const minutes = Math.floor(Math.abs(time - now) / 60_000)
  const direction = time < now ? -1 : 1
  let relative = 'just now'
  if (minutes >= 1440) relative = relativeTime.format(direction * Math.floor(minutes / 1440), 'day')
  else if (minutes >= 60) relative = relativeTime.format(direction * Math.floor(minutes / 60), 'hour')
  else if (minutes >= 1) relative = relativeTime.format(direction * minutes, 'minute')

  return { iso, utc, relative }
}

// formatTimeBefore states how long before the reference time a value is, in
// whole minutes under an hour, whole hours under two days, and whole days
// after that.
export function formatTimeBefore(value: string, reference: string): string {
  const gap = Date.parse(reference) - Date.parse(value)
  if (!Number.isFinite(gap) || gap < 0) return 'Unavailable'
  const minutes = Math.floor(gap / 60_000)
  if (minutes < 60) return countOf(minutes, 'minute')
  const hours = Math.floor(minutes / 60)
  if (hours < 48) return countOf(hours, 'hour')
  return countOf(Math.floor(hours / 24), 'day')
}

function countOf(count: number, unit: string): string {
  return `${count} ${unit}${count === 1 ? '' : 's'}`
}
