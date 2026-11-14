import { useCallback, useState } from 'react'
import { listBlockedPurchases, type BlockedPurchase, type BlockedPurchasePage, type SimulationRun } from '../api'
import { Timestamp } from '../Display'
import { formatUsdMinor } from '../displayFormat'
import { purchaseAddress } from '../purchaseAddress'
import { isActive } from '../simulation'
import { Button } from '../ui/Button'
import { usePolling } from '../usePolling'
import type { TimeDisplay } from '../useTimeDisplay'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'

const number = new Intl.NumberFormat('en-US')

// Shows the purchases of one run that scoring blocked, newest first, with the
// reason for each block. The page being viewed refreshes while the run is active.
export function BlockedPurchases({ run, time }: { run: SimulationRun; time: TimeDisplay }) {
  const pages = useBlockedPages(run)
  const { page, loadError, cursors } = pages
  return (
    <section className="panel simulation-blocked" aria-labelledby="simulation-blocked-title">
      <h2 id="simulation-blocked-title">Blocked purchases</h2>
      <p className="muted small">
        {number.format(run.blocked)} blocked in run <code className="simulation-id">{run.id}</code>. Open a
        transaction for its full evidence.
      </p>
      {loadError && (
        <ErrorState onRetry={pages.polling.retry} pending={pages.polling.pending} polite>
          {loadError}
        </ErrorState>
      )}
      {!page && !loadError && <LoadingSkeleton shape="table" label="Loading blocked purchases…" />}
      {page && page.items.length === 0 && cursors.length === 0 && (
        <EmptyState>No purchases in this run have been blocked.</EmptyState>
      )}
      {page && page.items.length > 0 && <BlockedTable items={page.items} time={time} />}
      {page && (cursors.length > 0 || page.next_cursor) && (
        <div className="button-row">
          <Button variant="secondary" size="sm" disabled={cursors.length === 0} onClick={pages.newer}>Newer</Button>
          <Button variant="secondary" size="sm" disabled={!page.next_cursor} onClick={pages.older}>Older</Button>
        </div>
      )}
    </section>
  )
}

// useBlockedPages keeps the cursors of the pages before the one being viewed,
// so Newer can step back without the server returning a previous cursor.
function useBlockedPages(run: SimulationRun) {
  const [page, setPage] = useState<BlockedPurchasePage | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [cursors, setCursors] = useState<string[]>([])
  const before = cursors.at(-1) ?? null
  const load = useCallback(
    (signal: AbortSignal) => listBlockedPurchases(run.id, before, signal),
    [run.id, before],
  )
  // The interval change when a run finishes triggers one more read, so the
  // final blocks appear.
  const polling = usePolling(load, {
    intervalMs: isActive(run.status) ? 3000 : undefined,
    waitAfterCompletion: true,
    onSuccess: (result) => {
      setPage(result)
      setLoadError(null)
    },
    onError: (error) => setLoadError(error instanceof Error ? error.message : 'Blocked purchases cannot be loaded.'),
  })

  function older() {
    const next = page?.next_cursor
    if (!next) return
    setPage(null)
    setCursors((current) => [...current, next])
  }

  function newer() {
    setPage(null)
    setCursors((current) => current.slice(0, -1))
  }

  return { page, loadError, cursors, polling, older, newer }
}

// On narrow screens each row is shown as a card. The explicit roles keep the
// table structure for assistive technology when CSS changes the display.
function BlockedTable({ items, time }: { items: BlockedPurchase[]; time: TimeDisplay }) {
  return (
    <div className="table-scroll" role="region" tabIndex={0} aria-label="Blocked purchases">
      <table className="stack-table" role="table">
        <caption className="visually-hidden">Blocked purchases of the run, newest first</caption>
        <thead role="rowgroup">
          <tr role="row">
            <th role="columnheader" scope="col">Transaction</th>
            <th role="columnheader" scope="col">Purchase time</th>
            <th role="columnheader" scope="col" className="num">Amount (USD)</th>
            <th role="columnheader" scope="col">Player</th>
            <th role="columnheader" scope="col">Card / IP country</th>
            <th role="columnheader" scope="col">Device</th>
            <th role="columnheader" scope="col">Why it was blocked</th>
          </tr>
        </thead>
        <tbody role="rowgroup">
          {items.map((item) => (
            <tr role="row" key={item.purchase_id}>
              <th role="rowheader" scope="row">
                <a href={purchaseAddress(item.purchase_id)} className="simulation-id">{item.transaction_id}</a>
              </th>
              <td role="cell" data-label="Purchase time"><Timestamp value={item.event_time} display={time} /></td>
              <td role="cell" data-label="Amount (USD)" className="num">{formatUsdMinor(item.amount_minor)}</td>
              <td role="cell" data-label="Player" className="simulation-id">{item.player_id}</td>
              <td role="cell" data-label="Card / IP country">
                {item.card_country ?? '—'} / {item.ip_country ?? '—'}
              </td>
              <td role="cell" data-label="Device" className="simulation-id">{item.device_id ?? 'Not sent'}</td>
              <td role="cell" data-label="Why it was blocked"><BlockReason item={item} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function BlockReason({ item }: { item: BlockedPurchase }) {
  if (item.reason === 'blocklist') {
    return <span>{item.fired_rules[0]?.message ?? 'Blocked by the blocklist'}</span>
  }
  return (
    <details className="block-reason">
      <summary>
        Rule score {number.format(item.rule_score)} reached the block threshold of{' '}
        {number.format(item.block_threshold)} (ruleset v{item.ruleset_version})
      </summary>
      <ul>
        {item.fired_rules.map((rule) => (
          <li key={rule.name}>
            <code>{rule.name}</code> +{rule.points}: {rule.message}
          </li>
        ))}
      </ul>
    </details>
  )
}
