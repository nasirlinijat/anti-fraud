import { useCallback, useState } from 'react'
import { listSimulationAnswers, type SimulationAnswer, type SimulationAnswerPage, type SimulationRun } from '../api'
import { purchaseAddress } from '../purchaseAddress'
import { isActive } from '../simulation'
import { Button } from '../ui/Button'
import { usePolling } from '../usePolling'
import { EmptyState, ErrorState, LoadingSkeleton } from '../ViewState'

const decisions = { allow: 'Allow', review: 'Review', block: 'Block' }
const answers = { fraud: 'Fraud', legitimate: 'Legitimate', unknown: 'Unknown' }

export function SimulationAnswers({ run }: { run: SimulationRun }) {
  const [revealed, setRevealed] = useState(false)
  const feed = useAnswerPages(run)
  const { page, error, exceptions, cursors, polling } = feed

  return (
    <section className="panel simulation-answers" aria-labelledby="simulation-answers-title">
      <h2 id="simulation-answers-title">Saved decisions</h2>
      <div className="button-row">
        <label><input type="checkbox" checked={revealed} onChange={event => setRevealed(event.target.checked)} /> Reveal answers</label>
        <label><input type="checkbox" checked={exceptions} onChange={event => feed.filter(event.target.checked)} /> Show misses and legitimate friction</label>
      </div>
      {error && <ErrorState onRetry={polling.retry} pending={polling.pending} polite>{error}</ErrorState>}
      {!page && !error && <LoadingSkeleton shape="table" label="Loading saved decisions…" />}
      {page && page.items.length === 0 && (
        <EmptyState>{exceptions ? 'No missed fraud or legitimate friction on this page.' : 'No saved decisions on this page.'}</EmptyState>
      )}
      {page && page.items.length > 0 && <AnswersTable items={page.items} revealed={revealed} />}
      {(cursors.length > 0 || page?.next_cursor) && (
        <nav className="button-row" aria-label="Saved decisions pagination">
          <Button variant="secondary" size="sm" disabled={!cursors.length} onClick={feed.newer}>Newer</Button>
          <Button variant="secondary" size="sm" disabled={!page?.next_cursor} onClick={feed.older}>Older</Button>
        </nav>
      )}
    </section>
  )
}

function useAnswerPages(run: SimulationRun) {
  const [page, setPage] = useState<SimulationAnswerPage | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [exceptions, setExceptions] = useState(false)
  const [cursors, setCursors] = useState<string[]>([])
  const before = cursors.at(-1)
  const load = useCallback(
    (signal: AbortSignal) => listSimulationAnswers(run.id, before, exceptions, signal),
    [run.id, before, exceptions],
  )
  const polling = usePolling(load, {
    intervalMs: isActive(run.status) ? 3000 : undefined,
    waitAfterCompletion: true,
    onSuccess: result => { setPage(result); setError(null) },
    onError: error => setError(error instanceof Error ? error.message : 'Saved decisions cannot be loaded.'),
  })

  function changePage(next: string[]) {
    polling.stop()
    setPage(null)
    setError(null)
    setCursors(next)
  }

  function filter(next: boolean) {
    changePage([])
    setExceptions(next)
  }

  function older() {
    if (page?.next_cursor) changePage([...cursors, page.next_cursor])
  }

  return { page, error, exceptions, cursors, polling, filter, older, newer: () => changePage(cursors.slice(0, -1)) }
}

function AnswersTable({ items, revealed }: { items: SimulationAnswer[]; revealed: boolean }) {
  return (
    <div className="table-scroll" role="region" aria-label="Simulation purchase comparison" tabIndex={0}>
      <table>
        <thead>
          <tr>
            <th scope="col">Transaction</th>
            <th scope="col">Scoring decision</th>
            {revealed && <th scope="col">Simulator answer</th>}
          </tr>
        </thead>
        <tbody>
          {items.map(item => (
            <tr key={item.purchase_id}>
              <th scope="row"><a className="simulation-id" href={purchaseAddress(item.purchase_id)}>{item.transaction_id}</a></th>
              <td><span className={`outcome outcome-${item.outcome}`}>{decisions[item.outcome]}</span></td>
              {revealed && <td>{answers[item.answer]}</td>}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
