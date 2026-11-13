import type { Ref } from 'react'
import type { CaseNetwork as Network, LinkedEntity, NetworkAnchor } from '../api'
import { entityLabels, linkedNoun } from './networkLayout'
import { sameSelection, visibleLinked, type NetworkInteraction } from './networkSelection'
import { StatusBadge } from './NetworkStatusMarks'

const number = new Intl.NumberFormat('en-US')

interface NetworkTableProps {
  network: Network
  interaction: NetworkInteraction
  // Each anchor's heading row gets this prefix and its type as its ID, so the
  // diagram's "more" node can move focus to it.
  rowIdPrefix: string
  detailsRef: Ref<HTMLDetailsElement>
}

export function NetworkTable({ network, interaction, rowIdPrefix, detailsRef }: NetworkTableProps) {
  return (
    <details className="network-details" ref={detailsRef}>
      <summary>View linked players, cards and devices as a table</summary>
      <div className="table-scroll" role="region" tabIndex={0} aria-label="Linked players, cards and devices">
        <table className="stack-table" role="table">
          <caption>Players and cards sharing this purchase&apos;s device, card or player within {network.window_days} days</caption>
          <thead role="rowgroup">
            <tr role="row">
              <th role="columnheader" scope="col">Linked value</th>
              <th role="columnheader" scope="col">Type</th>
              <th role="columnheader" scope="col" className="num">Shared purchases</th>
              <th role="columnheader" scope="col">Status</th>
            </tr>
          </thead>
          {network.anchors.map((anchor) => (
            <AnchorRows key={anchor.type} anchor={anchor} rowId={`${rowIdPrefix}-${anchor.type}`} interaction={interaction} />
          ))}
        </table>
      </div>
    </details>
  )
}

function AnchorRows({ anchor, rowId, interaction }: { anchor: NetworkAnchor; rowId: string; interaction: NetworkInteraction }) {
  const rows = visibleLinked(anchor, interaction.statuses)
  return (
    <tbody role="rowgroup">
      <tr role="row" className="network-anchor-row" id={rowId} tabIndex={-1}>
        <th role="rowheader" scope="rowgroup" colSpan={4}>{anchorHeading(anchor)}</th>
      </tr>
      {rows.length === 0 && <MessageRow>{noRowsText(anchor)}</MessageRow>}
      {rows.map((entity) => (
        <EntityRow key={`${entity.type}-${entity.value}`} anchor={anchor} entity={entity} interaction={interaction} />
      ))}
    </tbody>
  )
}

function noRowsText(anchor: NetworkAnchor): string {
  if (anchor.linked.length === 0) return `No other ${linkedNoun(anchor.type, 0)} share it.`
  return `No linked ${linkedNoun(anchor.type, 2)} with the selected status.`
}

function MessageRow({ children }: { children: string }) {
  return <tr role="row"><td role="cell" colSpan={4}>{children}</td></tr>
}

function EntityRow({ anchor, entity, interaction }: { anchor: NetworkAnchor; entity: LinkedEntity; interaction: NetworkInteraction }) {
  const selection = { anchor: anchor.type, entity }
  return (
    <tr role="row">
      <th role="rowheader" scope="row">
        <button type="button" className="btn-link" aria-pressed={sameSelection(interaction.selection, selection)}
          onClick={event => interaction.onSelect(selection, event.currentTarget)}>
          {entity.value}
        </button>
      </th>
      <td role="cell" data-label="Type">{entityLabels[entity.type]}</td>
      <td role="cell" data-label="Shared purchases" className="num">{number.format(entity.purchases)}</td>
      <td role="cell" data-label="Status"><StatusBadge status={entity.status} /></td>
    </tr>
  )
}

function anchorHeading(anchor: NetworkAnchor): string {
  const noun = linkedNoun(anchor.type, anchor.linked_total)
  const name = `${entityLabels[anchor.type]} ${anchor.value}${anchor.blocklisted ? ' (blocklisted)' : ''}`
  const listed = anchor.linked_total > anchor.linked.length ? `, ${number.format(anchor.linked.length)} listed` : ''
  return `${name}: ${number.format(anchor.linked_total)} linked ${noun}${listed}`
}
