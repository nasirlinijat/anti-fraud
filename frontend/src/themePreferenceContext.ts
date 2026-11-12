import { createContext, useContext } from 'react'
import type { ColorMode } from './themePreference'

interface ThemePreference {
  mode: ColorMode
  toggle: () => void
  error: string | null
}

export const ThemePreferenceContext = createContext<ThemePreference | null>(null)

export function useThemePreference() {
  const preference = useContext(ThemePreferenceContext)
  if (!preference) throw new Error('Theme controls require ConsoleTheme.')
  return preference
}
