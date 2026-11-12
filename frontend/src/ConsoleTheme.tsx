import { useEffect, useLayoutEffect, useState, type ReactNode } from 'react'
import { XUIProvider, useDesignSystem, getTypographyTokens } from '@xsolla/xui-core'
import { ThemePreferenceContext } from './themePreferenceContext'
import { createGlobalStyle } from 'styled-components'
import { colorMode, savedColorMode, systemColorMode, themeStorageKey, type ColorMode } from './themePreference'

export function ConsoleTheme({ children }: { children: ReactNode }) {
  const [initialPreference] = useState(savedColorMode)
  return <XUIProvider initialMode={initialPreference ?? systemColorMode()} initialProductContext="b2b">
    <ThemePreferenceProvider initialPreference={initialPreference}><ThemeTokens />{children}</ThemePreferenceProvider>
  </XUIProvider>
}

function ThemePreferenceProvider({ children, initialPreference }: { children: ReactNode; initialPreference: ColorMode | null }) {
  const { mode, setMode } = useDesignSystem()
  const [preference, setPreference] = useState(initialPreference)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const sync = () => setMode(preference ?? systemColorMode())
    sync()
    media.addEventListener('change', sync)
    return () => media.removeEventListener('change', sync)
  }, [preference, setMode])
  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key !== themeStorageKey && event.key !== null) return
      const next = colorMode(event.newValue)
      setPreference(next)
      setMode(next ?? systemColorMode())
      setError(null)
    }
    window.addEventListener('storage', sync)
    return () => window.removeEventListener('storage', sync)
  }, [setMode])
  useLayoutEffect(() => {
    document.querySelector('meta[name="color-scheme"]')?.setAttribute('content', mode)
  }, [mode])
  function toggle() {
    const next = mode === 'dark' ? 'light' : 'dark'
    setPreference(next)
    setMode(next)
    try {
      window.localStorage.setItem(themeStorageKey, next)
      setError(null)
    } catch {
      setError('Theme changed for this visit. Your choice could not be saved.')
    }
  }
  return <ThemePreferenceContext.Provider value={{ mode: mode === 'dark' ? 'dark' : 'light', toggle, error }}>{children}</ThemePreferenceContext.Provider>
}

function ThemeTokens() {
  const { mode, theme } = useDesignSystem()
  const colors = theme.colors
  const type = getTypographyTokens('b2b')
  return <ThemeVariables $mode={mode} $tokens={{
    '--bg': colors.background.secondary, '--surface': colors.background.primary,
    '--surface-subtle': colors.background.secondary, '--text': colors.content.primary,
    '--muted': colors.content.secondary, '--border': colors.border.secondary,
    '--control-border': colors.content.tertiary, '--accent': colors.control.brand.primary.bg,
    '--accent-text': colors.control.brand.text.primary, '--focus': colors.content.primary,
    '--allow': colors.content.success.primary, '--review': colors.content.warning.primary,
    '--block': colors.control.alert.text.tertiary, '--error-bg': colors.background.alert.secondary,
    '--allow-bg': colors.background.success.secondary, '--review-bg': colors.background.warning.secondary,
    '--block-bg': colors.background.alert.secondary, '--chart-allow': colors.content.success.primary,
    '--chart-review': colors.content.warning.primary, '--chart-block': colors.content.alert.primary,
    '--font-body': theme.fonts.body, '--font-heading': theme.fonts.heading,
    '--space-xs': `${theme.spacing.xs}px`, '--space-sm': `${theme.spacing.sm}px`,
    '--space-md': `${theme.spacing.md}px`, '--space-lg': `${theme.spacing.lg}px`, '--space-xl': `${theme.spacing.xl}px`,
    '--type-body': `${type.basic['body-md'].fontSize / 16}rem`,
    '--type-label': `${type.basic['body-sm'].fontSize / 16}rem`,
    '--type-note': `${type.basic['body-xs'].fontSize / 16}rem`,
    '--type-number': `${type.heading.h3.fontSize / 16}rem`,
    '--radius': `${theme.shape.cell.borderRadius}px`,
  }} />
}

const ThemeVariables = createGlobalStyle<{ $mode: string; $tokens: Record<string, string> }>`
  :root {
    ${props => Object.entries(props.$tokens).map(([key, value]) => `${key}: ${value};`).join('\n')}
    color-scheme: ${props => props.$mode};
    font-family: var(--font-body);
  }
`
