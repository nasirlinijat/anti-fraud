import type { ReviewCase } from '../api'
import { Timestamp } from '../Display'
import { accountEventLabels, formatTimeBefore } from '../displayFormat'
import type { TimeDisplay } from '../useTimeDisplay'
import { EmptyState } from '../ViewState'

// The player's logins and password resets in the 30 days before the purchase,
// newest first, as the game's account system reported them. Events recorded
// after the decision are marked, because the rules could not have seen them.
export function AccountEvents({ review, time }: { review: ReviewCase; time: TimeDisplay }) {
  if (review.account_events.length === 0) {
    return <EmptyState>No logins or password resets for this player in the 30 days before this purchase.</EmptyState>
  }
  return (
    <div className="table-scroll" role="region" tabIndex={0} aria-label="Recent account events">
      <table>
        <thead>
          <tr>
            <th scope="col">Event</th>
            <th scope="col">Event time</th>
            <th scope="col">Before the purchase</th>
            <th scope="col">Device</th>
            <th scope="col">IP country</th>
            <th scope="col">Recorded</th>
          </tr>
        </thead>
        <tbody>
          {review.account_events.map(event => (
            <tr key={event.event_id}>
              <th scope="row">{accountEventLabels[event.event_type]}</th>
              <td><Timestamp value={event.event_time} display={time} /></td>
              <td>{formatTimeBefore(event.event_time, review.event_time)}</td>
              <td>{event.device_id ?? 'None'}</td>
              <td>{event.ip_country ?? 'None'}</td>
              <td>
                <Timestamp value={event.received_at} display={time} />
                {event.recorded_after_decision && <> <span className="badge badge-muted">After the decision</span></>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
