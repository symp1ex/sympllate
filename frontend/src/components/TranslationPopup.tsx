import { useEffect, useState } from 'react'
import type { ClientConfig, Language, PopupState } from '../api'
import { errorMessage, translate } from '../api'
import { ErrorMessage } from './ErrorMessage'
import { LanguageSelect } from './LanguageSelect'
import { LoadingIndicator } from './LoadingIndicator'

const emptyState: PopupState = { source: 'auto', target: 'ru', sourceText: '', loading: false }

export function TranslationPopup({ languages, config }: { languages: Language[]; config: ClientConfig }) {
  const [state, setState] = useState<PopupState>(emptyState)

  useEffect(() => {
    let active = true
    void window.GetPopupState().then((value) => { if (active) setState(value) })
    const receive = (event: Event) => setState((event as CustomEvent<PopupState>).detail)
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') void window.HidePopup() }
    window.addEventListener('translator-popup', receive)
    window.addEventListener('keydown', escape)
    return () => { active = false; window.removeEventListener('translator-popup', receive); window.removeEventListener('keydown', escape) }
  }, [])

  const retry = async () => {
    if (state.loading) return
    setState((current) => ({ ...current, loading: true, error: undefined }))
    try {
      const result = await translate({ text: state.sourceText, source: state.source, target: state.target })
      setState((current) => ({ ...current, translatedText: result.text, detectedLanguage: result.detectedLanguage, loading: false }))
    } catch (caught) { setState((current) => ({ ...current, error: errorMessage(caught), loading: false })) }
  }

  const swap = () => setState((current) => ({ ...current, source: current.target, target: current.source === 'auto' ? config.defaultLanguagePair.second : current.source, sourceText: current.translatedText ?? '', translatedText: current.sourceText, error: undefined }))

  const copyTranslation = async () => {
    try { await window.CopyText(state.translatedText ?? '') }
    catch (caught) { setState((current) => ({ ...current, error: errorMessage(caught) })) }
  }

  return (
    <main className="popup-shell">
      <header className="popup-header"><strong>Быстрый перевод</strong><button className="icon-button" onClick={() => void window.HidePopup()} aria-label="Закрыть">×</button></header>
      <section className="language-row popup-languages">
        <LanguageSelect id="popup-source" label="Из" value={state.source || 'auto'} languages={languages} onChange={(source) => setState((current) => ({ ...current, source }))} disabled={state.loading} />
        <button className="swap" onClick={swap} disabled={state.loading} aria-label="Поменять языки местами">⇄</button>
        <LanguageSelect id="popup-target" label="На" value={state.target || 'ru'} languages={languages} onChange={(target) => setState((current) => ({ ...current, target }))} disabled={state.loading} allowAuto={false} />
      </section>
      {state.sourceText && <div className="popup-source" title={state.sourceText}>{state.sourceText}</div>}
      <div className="popup-result">{state.loading ? <LoadingIndicator /> : state.translatedText || (!state.error && 'Перевод появится здесь')}</div>
      {state.error && <ErrorMessage message={state.error} />}
      <div className="actions popup-actions">
        <button className="primary" onClick={() => void retry()} disabled={state.loading || !state.sourceText}>Перевести снова</button>
        <button onClick={() => void copyTranslation()} disabled={!state.translatedText}>Копировать</button>
        <button onClick={() => void window.HidePopup()}>Закрыть</button>
      </div>
    </main>
  )
}
