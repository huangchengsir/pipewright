/**
 * Naive UI theme overrides aligned to Pipewright design tokens.
 * Primary color = electric blue oklch(66% 0.155 258) ≈ #3B82F6-ish in sRGB.
 */

import { computed } from 'vue'
import { darkTheme, lightTheme } from 'naive-ui'
import type { GlobalThemeOverrides } from 'naive-ui'
import { useThemeStore } from '../stores/theme'

// Electric blue primary in sRGB hex (closest to oklch(66% 0.155 258))
const PRIMARY_DARK  = '#4B8EF0'   // oklch(66% 0.155 258) ≈
const PRIMARY_HOVER_DARK = '#3E7ED8'  // primary-press
const PRIMARY_LIGHT = '#2C6CD6'   // oklch(55% 0.17 258) ≈
const PRIMARY_HOVER_LIGHT = '#2258B8' // primary-press light

const darkOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: PRIMARY_DARK,
    primaryColorHover: PRIMARY_HOVER_DARK,
    primaryColorPressed: PRIMARY_HOVER_DARK,
    primaryColorSuppl: PRIMARY_DARK,
    borderRadius: '9px',
    fontFamily: '"Inter", ui-sans-serif, system-ui, "PingFang SC", sans-serif',
    fontFamilyMono: '"JetBrains Mono", ui-monospace, Menlo, monospace',
    fontSize: '13.5px',
    bodyColor: 'oklch(15% 0.003 270)',
    cardColor: 'oklch(20% 0.004 272)',
    modalColor: 'oklch(20% 0.004 272)',
    popoverColor: 'oklch(20% 0.004 272)',
    tableColor: 'oklch(20% 0.004 272)',
    inputColor: 'oklch(17.5% 0.004 270)',
    borderColor: 'oklch(100% 0 0 / 0.055)',
    dividerColor: 'oklch(100% 0 0 / 0.055)',
    textColorBase: 'oklch(95% 0.004 270)',
    textColor1: 'oklch(95% 0.004 270)',
    textColor2: 'oklch(72% 0.008 270)',
    textColor3: 'oklch(55% 0.01 272)',
  },
  Button: {
    colorPrimary: PRIMARY_DARK,
    colorHoverPrimary: PRIMARY_HOVER_DARK,
    colorPressedPrimary: PRIMARY_HOVER_DARK,
    colorFocusPrimary: PRIMARY_DARK,
    borderRadiusMedium: '9px',
    heightMedium: '34px',
  },
  Input: {
    borderRadius: '9px',
    heightMedium: '38px',
    color: 'oklch(17.5% 0.004 270)',
    colorFocus: 'oklch(17.5% 0.004 270)',
    border: '1px solid oklch(100% 0 0 / 0.055)',
    borderFocus: `1px solid ${PRIMARY_DARK}`,
    boxShadowFocus: `0 0 0 3px oklch(66% 0.155 258 / 0.2)`,
  },
}

const lightOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: PRIMARY_LIGHT,
    primaryColorHover: PRIMARY_HOVER_LIGHT,
    primaryColorPressed: PRIMARY_HOVER_LIGHT,
    primaryColorSuppl: PRIMARY_LIGHT,
    borderRadius: '9px',
    fontFamily: '"Inter", ui-sans-serif, system-ui, "PingFang SC", sans-serif',
    fontFamilyMono: '"JetBrains Mono", ui-monospace, Menlo, monospace',
    fontSize: '13.5px',
    bodyColor: 'oklch(96.5% 0.004 270)',
    cardColor: 'oklch(100% 0 0)',
    modalColor: 'oklch(100% 0 0)',
    popoverColor: 'oklch(100% 0 0)',
    tableColor: 'oklch(100% 0 0)',
    inputColor: 'oklch(97.5% 0.004 270)',
    borderColor: 'oklch(20% 0.02 270 / 0.1)',
    dividerColor: 'oklch(20% 0.02 270 / 0.1)',
    textColorBase: 'oklch(25% 0.01 270)',
    textColor1: 'oklch(25% 0.01 270)',
    textColor2: 'oklch(45% 0.014 270)',
    textColor3: 'oklch(58% 0.016 270)',
  },
  Button: {
    colorPrimary: PRIMARY_LIGHT,
    colorHoverPrimary: PRIMARY_HOVER_LIGHT,
    colorPressedPrimary: PRIMARY_HOVER_LIGHT,
    colorFocusPrimary: PRIMARY_LIGHT,
    borderRadiusMedium: '9px',
    heightMedium: '34px',
  },
  Input: {
    borderRadius: '9px',
    heightMedium: '38px',
    color: 'oklch(97.5% 0.004 270)',
    colorFocus: 'oklch(97.5% 0.004 270)',
    border: '1px solid oklch(20% 0.02 270 / 0.1)',
    borderFocus: `1px solid ${PRIMARY_LIGHT}`,
    boxShadowFocus: `0 0 0 3px oklch(55% 0.17 258 / 0.12)`,
  },
}

export function useNaiveTheme() {
  const themeStore = useThemeStore()

  const naiveTheme = computed(() =>
    themeStore.current === 'dark' ? darkTheme : lightTheme,
  )

  const naiveThemeOverrides = computed(() =>
    themeStore.current === 'dark' ? darkOverrides : lightOverrides,
  )

  return { naiveTheme, naiveThemeOverrides }
}
