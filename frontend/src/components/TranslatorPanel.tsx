import { useEffect, useRef, useState } from 'react'
import type { ClientConfig, Language } from '../api'
import { errorMessage, openImageBatchWindow, translate, translateImage } from '../api'
import {
  findClipboardImage,
  formatBytes,
  getSingleDroppedFile,
  prepareImageFile,
  releaseImagePreview,
} from '../imageInput'
import type { SourceInput } from '../imageInput'
import { ErrorMessage } from './ErrorMessage'
import { LanguageSelect } from './LanguageSelect'
import { LoadingIndicator } from './LoadingIndicator'

interface Props { config: ClientConfig; languages: Language[] }

const emptyTextSource: SourceInput = { kind: 'text', text: '' }

export function TranslatorPanel({ config, languages }: Props) {
  const [source, setSource] = useState('auto')
  const [target, setTarget] = useState(config.defaultLanguagePair.first)
  const [sourceInput, setSourceInput] = useState<SourceInput>(emptyTextSource)
  const [translatedText, setTranslatedText] = useState('')
  const [loading, setLoading] = useState(false)
  const [imageLoading, setImageLoading] = useState(false)
  const [dragging, setDragging] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const mounted = useRef(true)
  const imageSelection = useRef(0)
  const dragDepth = useRef(0)

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
      imageSelection.current += 1
    }
  }, [])

  useEffect(() => () => releaseImagePreview(sourceInput), [sourceInput])

  const selectImage = async (file: File) => {
    if (loading || imageLoading) return
    const selection = imageSelection.current + 1
    imageSelection.current = selection
    setError('')
    setNotice('')
    setImageLoading(true)
    try {
      const prepared = await prepareImageFile(file, config)
      if (!mounted.current || imageSelection.current !== selection) {
        releaseImagePreview(prepared)
        return
      }
      setSourceInput(prepared)
      setTranslatedText('')
    } catch (caught) {
      if (mounted.current && imageSelection.current === selection) setError(errorMessage(caught))
    } finally {
      if (mounted.current && imageSelection.current === selection) setImageLoading(false)
    }
  }

  const runTranslation = async () => {
    if (loading || imageLoading || (sourceInput.kind === 'text' && !sourceInput.text.trim())) return
    setError('')
    setNotice('')
    setLoading(true)
    try {
      const result = sourceInput.kind === 'image'
        ? await translateImage({
            dataBase64: sourceInput.dataBase64,
            mediaType: sourceInput.mediaType,
            source,
            target,
          })
        : await translate({ text: sourceInput.text, source, target })
      setTranslatedText(result.text)
      if (sourceInput.kind === 'image' && result.text === '') {
        setNotice('No text to translate was found in the image.')
      }
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setLoading(false)
    }
  }

  const swap = () => {
    setSource(target)
    setTarget(source === 'auto' ? config.defaultLanguagePair.second : source)
    if (sourceInput.kind === 'image') {
      setTranslatedText('')
    } else {
      setSourceInput({ kind: 'text', text: translatedText })
      setTranslatedText(sourceInput.text)
    }
    setError('')
    setNotice('')
  }

  const removeImage = () => {
    imageSelection.current += 1
    setSourceInput(emptyTextSource)
    setTranslatedText('')
    setError('')
    setNotice('')
  }

  const copyTranslation = async () => {
    setError('')
    try { await window.CopyText(translatedText) }
    catch (caught) { setError(errorMessage(caught)) }
  }

  const openBatchWindow = async () => {
    setError('')
    try { await openImageBatchWindow() }
    catch (caught) { setError(errorMessage(caught)) }
  }

  const handlePaste = (event: React.ClipboardEvent<HTMLElement>) => {
    const imageItem = findClipboardImage(event.clipboardData.items)
    if (!imageItem) return
    event.preventDefault()
    const file = imageItem.getAsFile()
    if (!file) {
      setError('The clipboard image is unavailable.')
      return
    }
    void selectImage(file)
  }

  const handleDrop = (event: React.DragEvent<HTMLElement>) => {
    event.preventDefault()
    event.stopPropagation()
    dragDepth.current = 0
    setDragging(false)
    if (loading || imageLoading) return
    try {
      void selectImage(getSingleDroppedFile(event.dataTransfer.files))
    } catch (caught) {
      setError(errorMessage(caught))
    }
  }

  const busy = loading || imageLoading
  const canTranslate = sourceInput.kind === 'image' || sourceInput.text.trim() !== ''

  return (
    <main
      className={`app-shell${dragging ? ' app-shell--dragging' : ''}`}
      onPaste={handlePaste}
      onKeyDown={(event) => {
        if (event.ctrlKey && event.key === 'Enter') {
          event.preventDefault()
          void runTranslation()
        }
      }}
      onDragEnter={(event) => {
        event.preventDefault()
        dragDepth.current += 1
        if (!busy) setDragging(true)
      }}
      onDragOver={(event) => {
        event.preventDefault()
        event.dataTransfer.dropEffect = busy ? 'none' : 'copy'
      }}
      onDragLeave={(event) => {
        event.preventDefault()
        dragDepth.current = Math.max(0, dragDepth.current - 1)
        if (dragDepth.current === 0) setDragging(false)
      }}
      onDrop={handleDrop}
    >
      {dragging && <div className="drop-overlay" aria-hidden="true">Drop one PNG or JPEG image</div>}
      <section className="language-row">
        <LanguageSelect id="source-language" label="Source language" value={source} languages={languages} onChange={setSource} disabled={busy} />
        <button
          className="swap"
          onClick={swap}
          disabled={busy}
          aria-label={sourceInput.kind === 'image' ? 'Swap languages' : 'Swap languages and texts'}
        >⇄</button>
        <LanguageSelect id="target-language" label="Target language" value={target} languages={languages} onChange={setTarget} disabled={busy} allowAuto={false} />
      </section>
      <section className="text-grid">
        {sourceInput.kind === 'text'
          ? <label><span>Source text</span><textarea value={sourceInput.text} maxLength={config.maxInputCharacters} onChange={(event) => setSourceInput({ kind: 'text', text: event.target.value })} placeholder="Enter text, paste an image, or drop an image…" disabled={busy} /></label>
          : <section className="source-image-panel" aria-label="Source image">
              <span>Source image</span>
              <div className="source-image-card">
                <img src={sourceInput.previewUrl} alt={sourceInput.fileName ? `Preview of ${sourceInput.fileName}` : 'Image preview'} />
                <div className="source-image-meta">
                  {sourceInput.fileName && <strong title={sourceInput.fileName}>{sourceInput.fileName}</strong>}
                  <span>{sourceInput.mediaType === 'image/png' ? 'PNG' : 'JPEG'} · {formatBytes(sourceInput.byteLength)}</span>
                </div>
                <button type="button" onClick={removeImage} disabled={busy}>Remove image</button>
              </div>
            </section>}
        <label><span>Translation</span><textarea value={translatedText} readOnly placeholder="The translation will appear here" /></label>
      </section>
      {error && <ErrorMessage message={error} />}
      {notice && <div className="notice" role="status">{notice}</div>}
      <div className="actions">
        <button className="primary" onClick={() => void runTranslation()} disabled={busy || !canTranslate}>{loading ? 'Translating…' : imageLoading ? 'Reading image…' : 'Translate'}</button>
        <button onClick={() => void copyTranslation()} disabled={!translatedText}>Copy</button>
        <button className="batch-window-launcher" type="button" onClick={() => void openBatchWindow()} aria-label="Open batch image translation" title="Batch image translation">
          <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
            <path d="M6.5 3.5h8.25a1.75 1.75 0 0 1 1.75 1.75v8.25" />
            <rect x="3.5" y="6.5" width="10.5" height="12" rx="1.5" />
            <circle cx="7" cy="10" r="1" />
            <path d="m5 16 2.75-3 1.9 2 1.4-1.5 2.95 3M17 8.5h4m-1.5-1.5L21 8.5 19.5 10M21 15.5h-4m1.5-1.5L17 15.5l1.5 1.5" />
          </svg>
        </button>
        {busy && <LoadingIndicator />}
        <span className="hint">Ctrl+V image · Drag-and-Drop · Ctrl+Enter</span>
      </div>
    </main>
  )
}
