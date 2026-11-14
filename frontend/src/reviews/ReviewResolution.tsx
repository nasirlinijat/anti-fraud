import { useId } from 'react'
import type { ResolvedReviewCase } from '../api'
import { Timestamp } from '../Display'
import type { TimeDisplay } from '../useTimeDisplay'

export function ReviewResolution({ resolution, time }: {
  resolution: ResolvedReviewCase['resolution']
  time: TimeDisplay
}) {
  const heading = useId()
  return (
    <section className="panel" aria-labelledby={heading}>
      <h3 id={heading}>Analyst decision</h3>
      <dl className="facts">
        <dt>Decision</dt><dd>{resolution.decision === 'approve' ? 'Approved' : 'Rejected'}</dd>
        <dt>Analyst</dt><dd>{resolution.analyst_id}</dd>
        <dt>Resolved time</dt><dd><Timestamp value={resolution.resolved_at} display={time} /></dd>
      </dl>
    </section>
  )
}
