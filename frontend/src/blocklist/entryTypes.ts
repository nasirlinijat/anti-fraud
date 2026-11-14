import type { EntryType } from '../api'

export const typeNames: Record<EntryType, string> = {
  card: 'Card token',
  device: 'Device ID',
  player: 'Player ID',
  ip: 'IP address',
}
