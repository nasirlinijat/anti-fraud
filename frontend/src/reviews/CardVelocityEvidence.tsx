import { useId, useState } from 'react'
import type { CardVelocityEvidence as Evidence, CardVelocityPurchase, FiredRule } from '../api'
import { Timestamp } from '../Display'
import { formatUsdMinor } from '../displayFormat'
import type { TimeDisplay } from '../useTimeDisplay'

export function CardVelocityEvidence({ rule, evidence, time }: {
  rule: FiredRule
  evidence?: Evidence
  time: TimeDisplay
}) {
  const heading = useId()
  const [showContext, setShowContext] = useState(Boolean(evidence?.records_available))
  const purchases = evidence?.purchases.filter(purchase => showContext || purchase.contributed) ?? []
  return (
    <section className="rule-evidence" aria-labelledby={heading}>
      <h5 id={heading}>Card velocity evidence</h5>
      {evidence ? (
        <>
          <dl className="facts">
            <dt>Saved observation</dt>
            <dd>{evidence.count} earlier {evidence.count === 1 ? 'purchase' : 'purchases'} on this card (limit {evidence.limit})</dd>
            <dt>Rule window</dt>
            <dd>After <Timestamp value={evidence.window_start} display={{ ...time, showUtc: true }} />{' '}
              through <Timestamp value={evidence.window_end} display={{ ...time, showUtc: true }} /></dd>
          </dl>
          <p>{rule.message}</p>
          {!evidence.records_available && <p>Record-level evidence is unavailable because the complete set of purchases cannot be verified.</p>}
          <div className="segmented" role="group" aria-label="Card velocity timeline view">
            <button type="button" aria-pressed={!showContext} onClick={() => setShowContext(false)}>At scoring</button>
            <button type="button" aria-pressed={showContext} onClick={() => setShowContext(true)}>What we know now</button>
          </div>
          {purchases.length > 0 && <CardVelocityTimeline purchases={purchases} time={time} />}
          {showContext && evidence.context_truncated && <p className="muted small">Only the most recent investigation records in this window are shown.</p>}
        </>
      ) : (
        <>
          {rule.message !== rule.name && <p>{rule.message}</p>}
          <p>This decision did not save individual purchase references. Record-level evidence is unavailable.</p>
        </>
      )}
    </section>
  )
}

function CardVelocityTimeline({ purchases, time }: { purchases: CardVelocityPurchase[]; time: TimeDisplay }) {
  return (
    <ol className="velocity-timeline" aria-label="Purchases in the card velocity window">
      {purchases.map(purchase => (
        <li key={purchase.transaction_id} className={purchase.contributed ? 'velocity-contributor' : 'velocity-context'}>
          <div><strong>{purchase.transaction_id}</strong> <span>{formatUsdMinor(purchase.amount_minor)}</span></div>
          <Timestamp value={purchase.event_time} display={time} />
          {purchase.contributed ? <strong>Counted at scoring</strong> : (
            <span>Investigation context · {purchase.recorded_after_decision ? 'Recorded after scoring' : 'Unverified at scoring'}</span>
          )}
        </li>
      ))}
    </ol>
  )
}
