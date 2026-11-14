import type { Outcome, RulesetPreview } from '../api'
import { OutcomeBadge, Timestamp } from '../Display'
import type { TimeDisplay } from '../useTimeDisplay'

const number = new Intl.NumberFormat('en-US')
const outcomes: Outcome[] = ['allow', 'review', 'block']
const outcomeNames: Record<Outcome, string> = { allow: 'Allowed', review: 'Sent to review', block: 'Blocked' }

function difference(before: number, after: number): string {
  const change = after - before
  if (change === 0) return 'no change'
  return `${change > 0 ? '+' : '−'}${number.format(Math.abs(change))}`
}

export function RulesetPreviewPanel({ preview, time }: { preview: RulesetPreview; time: TimeDisplay }) {
  return (
    <section className="panel ruleset-preview" aria-labelledby="ruleset-preview-heading">
      <h2 id="ruleset-preview-heading">Preview impact</h2>
      <p>
        Replayed the last {number.format(preview.decisions)} decisions with this draft:{' '}
        <strong>{preview.changed === 0 ? 'no outcome would change.' : `${number.format(preview.changed)} would change.`}</strong>{' '}
        Nothing was saved.
      </p>
      <div className="table-scroll" role="region" tabIndex={0} aria-label="Outcomes before and after">
        <table className="stack-table">
          <thead>
            <tr><th scope="col">Outcome</th><th scope="col" className="num">Now</th><th scope="col" className="num">With this draft</th><th scope="col" className="num">Change</th></tr>
          </thead>
          <tbody>
            {outcomes.map(outcome => (
              <tr key={outcome}>
                <th scope="row">{outcomeNames[outcome]}</th>
                <td className="num" data-label="Now">{number.format(preview.before[outcome])}</td>
                <td className="num" data-label="With this draft">{number.format(preview.after[outcome])}</td>
                <td className="num" data-label="Change">{difference(preview.before[outcome], preview.after[outcome])}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {preview.uncertain > 0 && (
        <p className="ruleset-preview-uncertain">
          {number.format(preview.uncertain)} {preview.uncertain === 1 ? 'decision' : 'decisions'} cannot be replayed exactly
          {preview.uncertain_rules.length > 0 && <>, because these rules were off or their settings changed: {preview.uncertain_rules.join(', ')}</>}
          {preview.missing_model_scores > 0 && <>{preview.uncertain_rules.length > 0 ? '; and ' : ', because '}{number.format(preview.missing_model_scores)} have no saved model score</>}
          . They are counted as if the rest of their decision stayed the same.
        </p>
      )}
      {preview.changes.length > 0 && (
        <div className="table-scroll" role="region" tabIndex={0} aria-label="Purchases whose outcome would change">
          <table className="stack-table">
            <thead>
              <tr><th scope="col">Transaction</th><th scope="col">Decided</th><th scope="col">Now</th><th scope="col">With this draft</th><th scope="col" className="num">Rule score</th></tr>
            </thead>
            <tbody>
              {preview.changes.map(change => (
                <tr key={change.purchase_id}>
                  <th scope="row">{change.transaction_id}{change.uncertain && <span className="badge badge-muted">not exact</span>}</th>
                  <td data-label="Decided"><Timestamp value={change.decided_at} display={time} /></td>
                  <td data-label="Now"><OutcomeBadge outcome={change.before} /></td>
                  <td data-label="With this draft"><OutcomeBadge outcome={change.after} /></td>
                  <td className="num" data-label="Rule score">{change.rule_score_before} → {change.rule_score_after}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {preview.changed > preview.changes.length && (
            <p className="muted small">Showing the newest {preview.changes.length} of {number.format(preview.changed)} changed purchases.</p>
          )}
        </div>
      )}
    </section>
  )
}
