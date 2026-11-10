import { useId } from 'react'
import type { Outcome } from './api'
import { formatTimestamp, outcomeLabels } from './displayFormat'
import type { TimeDisplay } from './useTimeDisplay'

export function OutcomeBadge({ outcome }: { outcome: Outcome }) {
  return <span className={`outcome outcome-${outcome}`}>{outcomeLabels[outcome]}</span>
}

const scoreNumber = new Intl.NumberFormat('en-US')

export function RuleScoreBadge({ value }: { value: number }) {
  return <span className="score-badge">{scoreNumber.format(value)} {value === 1 ? 'point' : 'points'}</span>
}

export function ModelRiskBadge({ value }: { value: number | null }) {
  return <span className="score-badge">{value === null ? 'Unavailable' : value.toFixed(4)}</span>
}

export function TimeDisplayToggle({ display }: { display: TimeDisplay }) {
  const id = useId()
  return (
    <div className="time-display-toggle">
      <input id={id} type="checkbox" checked={display.showUtc}
        onChange={event => display.setShowUtc(event.currentTarget.checked)} />
      <label htmlFor={id}>Show UTC times</label>
    </div>
  )
}

export function Timestamp({ value, display }: { value: string; display: TimeDisplay }) {
  const time = formatTimestamp(value, display.now)
  if (!time) return <span>Unavailable</span>
  return <time dateTime={time.iso} title={display.showUtc ? undefined : time.utc}>{display.showUtc ? time.utc : time.relative}</time>
}
