export type ColorMode = 'light' | 'dark'
export const themeStorageKey = 'anti-fraud.theme'

export function colorMode(value: string | null): ColorMode | null {
  return value === 'light' || value === 'dark' ? value : null
}

export function savedColorMode(): ColorMode | null {
  try {
    return colorMode(window.localStorage.getItem(themeStorageKey))
  } catch {
    // A blocked store behaves like a first visit; switching still works in memory.
    return null
  }
}

export function systemColorMode(): ColorMode {
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}
