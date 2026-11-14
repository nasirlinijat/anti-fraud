export interface ModelReason {
  feature: string
  value: number
  effect: number
}

type Kind = 'money' | 'count' | 'seconds' | 'days' | 'share' | 'ratio' | 'flag' | 'code' | 'number'

const FEATURES: Record<string, [label: string, kind: Kind]> = {
  amount: ['Amount', 'money'],
  cents: ['Cents of the amount', 'number'],
  product_code: ['Product type', 'code'],
  card_network: ['Card network', 'code'],
  card_type: ['Card type', 'code'],
  player_txn_count: ["Player's earlier purchases", 'count'],
  player_avg_amount: ["Player's average amount", 'money'],
  amount_to_avg: ["Amount compared with the player's average", 'ratio'],
  secs_since_last: ["Time since the player's last purchase", 'seconds'],
  secs_since_first: ["Time since the player's first purchase", 'seconds'],
  card_uses_1h: ['Card uses in the last hour', 'count'],
  card_uses_24h: ['Card uses in the last 24 hours', 'count'],
  player_distinct_cards: ['Cards the player has used', 'count'],
  email_is_new_for_player: ['Email is new for the player', 'flag'],
  card_country: ['Card country', 'code'],
  card_category: ['Card category', 'code'],
  billing_country: ['Billing country', 'code'],
  email_domain_share: ['Share of purchases from this email domain', 'share'],
  has_recipient_email: ['Has a recipient email', 'flag'],
  recipient_email_differs: ['Recipient email differs from the buyer', 'flag'],
  player_max_amount: ["Player's largest earlier amount", 'money'],
  player_uses_1h: ['Player purchases in the last hour', 'count'],
  player_uses_24h: ['Player purchases in the last 24 hours', 'count'],
  player_same_amount_count: ['Player purchases with the same amount', 'count'],
  player_distinct_emails: ['Emails the player has used', 'count'],
  email_players_share: ['Share of players with this email', 'share'],
  has_device_info: ['Has device details', 'flag'],
  device_type: ['Device type', 'code'],
  device_info_share: ['Share of purchases from this device model', 'share'],
  device_players_share: ['Share of players on this device model', 'share'],
  device_is_new_for_player: ['Device is new for the player', 'flag'],
  player_distinct_devices: ['Devices the player has used', 'count'],
  card_distinct_players: ['Players who used this card', 'count'],
  card1_distinct_players: ['Players with this card issuer', 'count'],
  card1_distinct_addresses: ['Billing addresses with this card issuer', 'count'],
  card1_distinct_emails: ['Emails with this card issuer', 'count'],
  billing_distance: ['Billing distance', 'number'],
  account_age_days: ['Account age', 'days'],
  has_client_info: ['Has browser details', 'flag'],
  os_family: ['Operating system', 'code'],
  browser_family: ['Browser', 'code'],
  os_share: ['Share of purchases from this operating system', 'share'],
  browser_share: ['Share of purchases from this browser', 'share'],
  screen_share: ['Share of purchases with this screen size', 'share'],
  screen_area: ['Screen area', 'number'],
  os_is_new_for_player: ['Operating system is new for the player', 'flag'],
  browser_is_new_for_player: ['Browser is new for the player', 'flag'],
  player_distinct_browsers: ['Browsers the player has used', 'count'],
  card_issuer_code: ['Card issuer', 'code'],
  billing_region_code: ['Billing region', 'code'],
  ip_country_differs_from_card: ["IP country differs from the card's country", 'flag'],
  device_id_is_new_for_player: ['Device ID is new for the player', 'flag'],
  device_distinct_players: ['Players on this device', 'count'],
  billing_region_is_new_for_card: ['Billing region is new for the card', 'flag'],
}

const MISSING = -1
const number = new Intl.NumberFormat('en-US', { maximumFractionDigits: 2 })
const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' })
const percent = new Intl.NumberFormat('en-US', { style: 'percent', maximumFractionDigits: 1 })

export function reasonLabel(feature: string): string {
  return FEATURES[feature]?.[0] ?? feature.replaceAll('_', ' ').replace(/^./, c => c.toUpperCase())
}

function duration(seconds: number): string {
  if (seconds < 60) return `${number.format(seconds)} s`
  if (seconds < 3_600) return `${number.format(Math.round(seconds / 60))} min`
  if (seconds < 172_800) return `${number.format(Math.round(seconds / 3_600))} h`
  return `${number.format(Math.round(seconds / 86_400))} days`
}

export function reasonValue(feature: string, value: number): string | null {
  const kind = FEATURES[feature]?.[1] ?? 'number'
  if (kind === 'code') return null
  if (value === MISSING) return 'Not known'
  switch (kind) {
    case 'money': return money.format(value)
    case 'count': return number.format(value)
    case 'seconds': return duration(value)
    case 'days': return `${number.format(value)} ${value === 1 ? 'day' : 'days'}`
    case 'share': return percent.format(value)
    case 'ratio': return `${number.format(value)}×`
    case 'flag': return value >= 0.5 ? 'Yes' : 'No'
    default: return number.format(value)
  }
}

export function reasonStrength(reason: ModelReason, reasons: ModelReason[]): number {
  const strongest = Math.max(...reasons.map(r => Math.abs(r.effect)))
  return strongest > 0 ? Math.abs(reason.effect) / strongest : 0
}
