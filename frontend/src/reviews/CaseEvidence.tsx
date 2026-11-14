import { useId, useState, type ReactNode } from 'react'
import type { AvsResult, CvvResult, IpIntel, ReviewCase, ThreeDsResult } from '../api'
import { ModelRiskBadge, OutcomeBadge, RuleScoreBadge, Timestamp } from '../Display'
import { formatUsdMinor } from '../displayFormat'
import type { TimeDisplay } from '../useTimeDisplay'
import { EmptyState } from '../ViewState'
import { AccountEvents } from './AccountEvents'
import { CardVelocityEvidence } from './CardVelocityEvidence'
import { reasonLabel, reasonStrength, reasonValue, type ModelReason } from './modelReasons'

export function CaseEvidence({ review, time, children }: { review: ReviewCase; time: TimeDisplay; children?: ReactNode }) {
  const id = useId()
  return (
    <>
      <section className="case-evidence" aria-labelledby={`${id}-scoring-evidence`}>
        <h3 id={`${id}-scoring-evidence`}>Evidence at scoring</h3>
        <div className="case-grid">
          <section className="panel" aria-labelledby={`${id}-purchase`}>
            <h4 id={`${id}-purchase`}>Purchase</h4>
            <PurchaseFacts review={review} time={time} />
          </section>
          <section className="panel" aria-labelledby={`${id}-scoring`}>
            <h4 id={`${id}-scoring`}>Scoring</h4>
            <ScoringFacts review={review} />
          </section>
        </div>
        <section className="panel" aria-labelledby={`${id}-verification`}>
          <h4 id={`${id}-verification`}>Payment verification</h4>
          <VerificationFacts review={review} />
        </section>
        <section className="panel" aria-labelledby={`${id}-ip-intel`}>
          <h4 id={`${id}-ip-intel`}>IP intelligence</h4>
          <IpIntelFacts intel={review.ip_intel} />
        </section>
        <section className="panel" aria-labelledby={`${id}-rules`}>
          <h4 id={`${id}-rules`}>Fired rules</h4>
          <FiredRules review={review} time={time} />
        </section>
        {review.model_score !== null && (
          <section className="panel" aria-labelledby={`${id}-reasons`}>
            <h4 id={`${id}-reasons`}>Why this model risk</h4>
            <ModelReasons reasons={review.model_reasons ?? []} />
          </section>
        )}
      </section>
      <section className="case-evidence" aria-labelledby={`${id}-context`}>
        <h3 id={`${id}-context`}>Investigation context</h3>
        <section className="panel" aria-labelledby={`${id}-history`}>
          <h4 id={`${id}-history`}>Recent player purchases</h4>
          <PlayerPurchases review={review} time={time} />
        </section>
        <section className="panel" aria-labelledby={`${id}-device`}>
          <h4 id={`${id}-device`}>Recent device activity</h4>
          <DeviceActivity review={review} time={time} />
        </section>
        <section className="panel" aria-labelledby={`${id}-account`}>
          <h4 id={`${id}-account`}>Recent account events</h4>
          <AccountEvents review={review} time={time} />
        </section>
        {children}
      </section>
    </>
  )
}

function PurchaseFacts({ review, time }: { review: ReviewCase; time: TimeDisplay }) {
  return (
    <dl className="facts">
      <dt>Transaction</dt>
      <dd>{review.transaction_id}</dd>
      <dt>Player</dt>
      <dd>{review.player_id}</dd>
      <dt>Device</dt>
      <dd>{review.device_id ?? 'None'}</dd>
      <dt>Amount (USD)</dt>
      <dd>{formatUsdMinor(review.amount_minor)}</dd>
      <dt>Purchase time</dt>
      <dd><Timestamp value={review.event_time} display={time} /></dd>
      <dt>Queued time</dt>
      <dd><Timestamp value={review.queued_at} display={time} /></dd>
    </dl>
  )
}

function ScoringFacts({ review }: { review: ReviewCase }) {
  return (
    <dl className="facts">
      <dt>Rule score</dt>
      <dd><RuleScoreBadge value={review.rule_score} /></dd>
      <dt>Ruleset version</dt>
      <dd>{review.ruleset_version}</dd>
      <dt>Model risk</dt>
      <dd><ModelRiskBadge value={review.model_score} /></dd>
      <dt>Model version</dt>
      <dd>{review.model_version ?? 'None'}</dd>
    </dl>
  )
}

const verificationLabels: Record<CvvResult | AvsResult | ThreeDsResult, string> = {
  match: 'Match',
  partial: 'Partial match',
  mismatch: 'Mismatch',
  not_checked: 'Not checked',
  authenticated: 'Authenticated',
  attempted: 'Attempted',
  failed: 'Failed',
  not_enrolled: 'Not enrolled',
}

function verificationLabel(result: CvvResult | AvsResult | ThreeDsResult | null): string {
  return result ? verificationLabels[result] : 'Not sent'
}

function VerificationFacts({ review }: { review: ReviewCase }) {
  return (
    <dl className="facts">
      <dt>Card security code (CVV)</dt>
      <dd>{verificationLabel(review.cvv_result)}</dd>
      <dt>Billing address (AVS)</dt>
      <dd>{verificationLabel(review.avs_result)}</dd>
      <dt>3-D Secure</dt>
      <dd>{verificationLabel(review.three_ds_result)}</dd>
    </dl>
  )
}

function networkKinds(intel: IpIntel): string {
  const kinds = [
    intel.hosting && 'Hosting or data centre',
    intel.vpn && 'VPN',
    intel.proxy && 'Public proxy',
    intel.tor && 'Tor exit',
  ].filter((kind): kind is string => typeof kind === 'string')
  return kinds.length > 0 ? kinds.join(', ') : 'Ordinary address'
}

function IpIntelFacts({ intel }: { intel: IpIntel | null | undefined }) {
  if (!intel) return <EmptyState>No IP lookup was made: the lookup is off or the purchase had no IP address.</EmptyState>
  return (
    <dl className="facts">
      <dt>Provider</dt>
      <dd>{intel.provider}</dd>
      <dt>Network</dt>
      <dd>{intel.available ? networkKinds(intel) : 'Unknown: the lookup failed or timed out'}</dd>
    </dl>
  )
}

function FiredRules({ review, time }: { review: ReviewCase; time: TimeDisplay }) {
  const [expanded, setExpanded] = useState(false)
  const detailsId = useId()
  const cardVelocity = review.fired_rules.find(rule => rule.name === 'card_velocity')
  if (review.fired_rules.length === 0) return <EmptyState>No rules fired.</EmptyState>
  return (
    <>
      <div className="table-scroll" role="region" tabIndex={0} aria-label="Fired rule evidence">
        <table>
          <thead>
            <tr>
              <th scope="col">Rule</th>
              <th scope="col">Why it fired</th>
              <th scope="col" className="num">Points</th>
            </tr>
          </thead>
          <tbody>
            {review.fired_rules.map(rule => (
              <tr key={rule.name}>
                <th scope="row">
                  {rule.name === 'card_velocity' ? (
                    <button className="rule-evidence-trigger" type="button" aria-expanded={expanded} aria-controls={detailsId}
                      onClick={() => setExpanded(current => !current)}>Card velocity</button>
                  ) : <code>{rule.name}</code>}
                </th>
                <td>{rule.message}</td>
                <td className="num">{rule.points}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {cardVelocity && (
        <div id={detailsId} hidden={!expanded}>
          {expanded && <CardVelocityEvidence rule={cardVelocity} evidence={review.card_velocity_evidence} time={time} />}
        </div>
      )}
    </>
  )
}

function PlayerPurchases({ review, time }: { review: ReviewCase; time: TimeDisplay }) {
  if (review.player_purchases.length === 0) return <EmptyState>No earlier purchases for this player.</EmptyState>
  return (
    <div className="table-scroll" role="region" tabIndex={0} aria-label="Recent player purchases">
      <table>
        <thead>
          <tr>
            <th scope="col">Transaction</th>
            <th scope="col">Purchase time</th>
            <th scope="col" className="num">Amount (USD)</th>
            <th scope="col">Outcome</th>
          </tr>
        </thead>
        <tbody>
          {review.player_purchases.map(purchase => (
            <tr key={purchase.transaction_id}>
              <th scope="row">{purchase.transaction_id}</th>
              <td><Timestamp value={purchase.event_time} display={time} /></td>
              <td className="num">{formatUsdMinor(purchase.amount_minor)}</td>
              <td><OutcomeBadge outcome={purchase.outcome} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function DeviceActivity({ review, time }: { review: ReviewCase; time: TimeDisplay }) {
  const activity = review.device_activity
  if (activity === null) return <EmptyState>This purchase did not include a device ID.</EmptyState>
  return (
    <dl className="facts">
      <dt>Purchases on this device</dt>
      <dd>{activity.purchases}</dd>
      <dt>Distinct cards</dt>
      <dd>{activity.distinct_cards}</dd>
      <dt>Last seen</dt>
      <dd><Timestamp value={activity.last_seen_at} display={time} /></dd>
    </dl>
  )
}

function ModelReasons({ reasons }: { reasons: ModelReason[] }) {
  if (reasons.length === 0) {
    return <EmptyState>Model reasons are unavailable for this score.</EmptyState>
  }
  return (
    <>
      <p className="muted small">What moved this purchase's own model score the most, strongest first.</p>
      <ol className="model-reasons">
        {reasons.map(reason => {
          const raises = reason.effect > 0
          const value = reasonValue(reason.feature, reason.value)
          return (
            <li key={reason.feature} className={raises ? 'model-reason-up' : 'model-reason-down'}>
              <span className="model-reason-arrow" aria-hidden>{raises ? '▲' : '▼'}</span>
              <span className="model-reason-text">
                <span>{reasonLabel(reason.feature)}{value !== null && <>: <strong>{value}</strong></>}</span>
                <span className="muted small">{raises ? 'Raises the risk' : 'Lowers the risk'}</span>
              </span>
              <span className="model-reason-bar" aria-hidden>
                <span style={{ inlineSize: `${Math.max(4, Math.round(reasonStrength(reason, reasons) * 100))}%` }} />
              </span>
            </li>
          )
        })}
      </ol>
    </>
  )
}
