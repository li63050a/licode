export type ThemeMode = 'light' | 'dark'

const KEY = 'licode_theme'

export function useTheme() {
  const mode = useState<ThemeMode>('theme', () => 'light')

  function apply(m: ThemeMode) {
    mode.value = m
    if (import.meta.client) {
      document.documentElement.classList.toggle('dark', m === 'dark')
      localStorage.setItem(KEY, m)
    }
  }

  function initTheme() {
    if (import.meta.client) {
      const saved = localStorage.getItem(KEY) as ThemeMode | null
      apply(saved === 'dark' ? 'dark' : 'light')
    }
  }

  function toggleTheme() {
    apply(mode.value === 'dark' ? 'light' : 'dark')
  }

  return { mode, initTheme, toggleTheme }
}
