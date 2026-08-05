import { useEffect, useState } from 'react'
import type { ClientConfig, Language, PopupState } from '../api'
import { errorMessage } from '../api'
import { ErrorMessage } from './ErrorMessage'
import { LanguageSelect } from './LanguageSelect'
import { LoadingIndicator } from './LoadingIndicator'

const emptyState: PopupState = { source: 'auto', target: 'ru', loading: false }

export function TranslationPopup({ languages }: { languages: Language[]; config: ClientConfig }) {
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

  const changeTarget = (target: string) => {
    setState((current) => ({ ...current, target, translatedText: undefined, loading: true, error: undefined }))
    void window.SetQuickTranslationTarget(target).catch((caught: unknown) => {
      setState((current) => ({ ...current, translatedText: undefined, loading: false, error: errorMessage(caught) }))
    })
  }

  const copyTranslation = async () => {
    try { await window.CopyText(state.translatedText ?? '') }
    catch (caught) { setState((current) => ({ ...current, error: errorMessage(caught) })) }
  }

  return (
    <main className="popup-shell">
      <section className="language-row popup-languages">
        <LanguageSelect id="popup-from-language" label="Из" value={state.source || 'auto'} languages={languages} onChange={() => {}} disabled />
        <LanguageSelect id="popup-target" label="На" value={state.target || 'ru'} languages={languages} onChange={changeTarget} allowAuto={false} />
      </section>
      <div className="popup-result">{state.loading ? <LoadingIndicator /> : state.translatedText || (!state.error && 'Перевод появится здесь')}</div>
      {state.error && <ErrorMessage message={state.error} />}
      <div className="actions popup-actions">
        <button onClick={() => void copyTranslation()} disabled={state.loading || !state.translatedText}>Копировать</button>
        <button onClick={() => void window.HidePopup()}>Закрыть</button>
      </div>
    </main>
  )
}
