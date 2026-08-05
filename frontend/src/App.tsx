import { useEffect, useState } from 'react'
import type { ClientConfig, Language } from './api'
import { errorMessage } from './api'
import { ErrorMessage } from './components/ErrorMessage'
import { LoadingIndicator } from './components/LoadingIndicator'
import { SettingsPanel } from './components/SettingsPanel'
import { TranslationPopup } from './components/TranslationPopup'
import { TranslatorPanel } from './components/TranslatorPanel'
import { WindowChrome } from './components/WindowChrome'

type WindowMode = 'main' | 'popup'
type MainView = 'main' | 'settings'
type ReadyState = { config: ClientConfig; languages: Language[] }

export default function App() {
  const [mode, setMode] = useState<WindowMode | null>(null)
  const [ready, setReady] = useState<ReadyState | null>(null)
  const [view, setView] = useState<MainView>('main')
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    const initialize = async () => {
      try {
        const windowMode = await window.GetWindowMode()
        const initialView = windowMode === 'main' ? await window.GetInitialView() : 'main'
        if (!active) return
        setMode(windowMode)
        setView(initialView)
        const [config, languages] = await Promise.all([window.GetConfig(), window.GetSupportedLanguages()])
        if (active) setReady({ config, languages })
      } catch (caught) {
        if (active) setError(errorMessage(caught))
      }
    }
    void initialize()
    return () => { active = false }
  }, [])

  useEffect(() => {
    const changeView = (event: Event) => {
      const detail = (event as CustomEvent<unknown>).detail
      if (detail === 'main' || detail === 'settings') setView(detail)
    }
    window.addEventListener('sympllate-view', changeView)
    return () => window.removeEventListener('sympllate-view', changeView)
  }, [])

  const content = error
    ? <main className="centered"><ErrorMessage message={error} /></main>
    : ready
      ? mode === 'popup'
        ? <TranslationPopup languages={ready.languages} config={ready.config} />
        : view === 'settings'
          ? <SettingsPanel onBack={() => setView('main')} />
          : <TranslatorPanel config={ready.config} languages={ready.languages} />
      : <main className="centered"><LoadingIndicator /></main>

  if (!mode) return content
  return (
    <WindowChrome
      mode={mode}
      onSettings={mode === 'main' ? () => setView('settings') : undefined}
      lockContentOverflow={mode === 'main' && view === 'settings'}
    >
      {content}
    </WindowChrome>
  )
}
