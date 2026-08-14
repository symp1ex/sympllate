import { useEffect, useRef, useState } from 'react'
import type { BatchSelection, ImageBatchStatus, Language } from '../api'
import { cancelImageBatch, errorMessage, pollImageBatch, selectBatchImageDirectory, selectBatchImageFiles, startImageBatch } from '../api'
import { imageBatchProgress, imageBatchStageLabel, imageBatchStateLabel } from '../imageBatchState'
import { defaultConcreteSource } from '../languageDefaults'
import { ErrorMessage } from './ErrorMessage'
import { LanguageSelect } from './LanguageSelect'

interface Props {
  languages: Language[]
  defaultSource: string
  defaultTarget: string
  disabled: boolean
  onBusyChange: (busy: boolean) => void
}

export function ImageBatchPanel({ languages, defaultSource, defaultTarget, disabled, onBusyChange }: Props) {
  const [selection, setSelection] = useState<BatchSelection | null>(null)
  const [source, setSource] = useState(() => defaultConcreteSource(languages, defaultSource))
  const [target, setTarget] = useState(defaultTarget)
  const [debug, setDebug] = useState(false)
  const [selecting, setSelecting] = useState(false)
  const [jobID, setJobID] = useState('')
  const [status, setStatus] = useState<ImageBatchStatus | null>(null)
  const [error, setError] = useState('')
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; onBusyChange(false) }
  }, [onBusyChange])

  useEffect(() => {
    if (!jobID) return
    const controller = new AbortController()
    void pollImageBatch(jobID, (next) => {
      if (mounted.current) setStatus(next)
    }, controller.signal).then((finalStatus) => {
      if (!mounted.current) return
      setStatus(finalStatus)
      setJobID('')
      setSelection(null)
      onBusyChange(false)
    }).catch((caught) => {
      if (!mounted.current || controller.signal.aborted) return
      setError(errorMessage(caught))
      setJobID('')
      onBusyChange(false)
    })
    return () => controller.abort()
  }, [jobID, onBusyChange])

  const choose = async (kind: 'files' | 'directory') => {
    if (disabled || selecting || jobID) return
    setSelecting(true)
    setError('')
    try {
      const next = kind === 'files' ? await selectBatchImageFiles() : await selectBatchImageDirectory()
      if (mounted.current && next.fileCount > 0 && next.id) { setSelection(next); setStatus(null) }
    } catch (caught) {
      if (mounted.current) setError(errorMessage(caught))
    } finally {
      if (mounted.current) setSelecting(false)
    }
  }

  const start = async () => {
    if (!selection || !source || disabled || selecting || jobID) return
    setError('')
    try {
      const id = await startImageBatch({ selectionId: selection.id, source, target, debug })
      if (!mounted.current) return
      setStatus({ id, state: 'pending', total: selection.fileCount, processed: 0, translated: 0, rendered: 0, partial: 0, warnings: 0, noText: 0, failed: 0 })
      setJobID(id)
      onBusyChange(true)
    } catch (caught) {
      if (mounted.current) setError(errorMessage(caught))
    }
  }

  const cancel = async () => {
    if (!jobID) return
    try { await cancelImageBatch(jobID) }
    catch (caught) { if (mounted.current) setError(errorMessage(caught)) }
  }

  const active = jobID !== ''
  const progress = status ? imageBatchProgress(status) : 0

  return (
    <section className="batch-panel" aria-labelledby="batch-heading">
      <div className="batch-heading-row">
        <div><h2 id="batch-heading">Batch image translation</h2><p>OCR and translate PNG/JPEG files without changing the originals.</p></div>
        <div className="batch-options">
          <label className="batch-debug"><input type="checkbox" checked={debug} onChange={(event) => setDebug(event.target.checked)} disabled={active || disabled} /> OCR debug images</label>
        </div>
      </div>
      <div className="batch-picker-row">
        <button type="button" onClick={() => void choose('files')} disabled={active || disabled || selecting}>Select images</button>
        <button type="button" onClick={() => void choose('directory')} disabled={active || disabled || selecting}>Select directory</button>
        <span className="batch-selection" title={selection?.displayName}>{selection ? `Selected: ${selection.fileCount} · ${selection.displayName}` : 'No images selected'}</span>
      </div>
      <div className="batch-language-row">
        <LanguageSelect id="batch-source-language" label="Source language" value={source} languages={languages} onChange={setSource} disabled={active || disabled} allowAuto={false} />
        <LanguageSelect id="batch-target-language" label="Target language" value={target} languages={languages} onChange={setTarget} disabled={active || disabled} allowAuto={false} />
        {active
          ? <button type="button" onClick={() => void cancel()}>Cancel</button>
          : <button className="primary" type="button" onClick={() => void start()} disabled={!selection || !source || disabled || selecting}>Start batch</button>}
      </div>
      {status && <div className="batch-status" role="status">
        <div><strong>{imageBatchStateLabel(status.state)}</strong><span>{status.processed} of {status.total}</span></div>
        <progress max={100} value={progress}>{progress}%</progress>
        {status.currentFile && <div className="batch-current" title={status.currentFile}>Current file: {status.currentFile}{status.currentStage ? ` · ${imageBatchStageLabel(status.currentStage)}` : ''}</div>}
        <div className="batch-counts"><span>Translated: {status.translated}</span><span>Rendered: {status.rendered}</span><span>Partial: {status.partial}</span><span>No text: {status.noText}</span><span>Warnings: {status.warnings}</span><span>Errors: {status.failed}</span></div>
        {status.outputDirectory && (status.state === 'completed' || status.state === 'completed_with_errors') && <div className="batch-output" title={status.outputDirectory}>Opened: {status.outputDirectory}</div>}
        {status.error && <ErrorMessage message={status.error} />}
      </div>}
      {error && <ErrorMessage message={error} />}
    </section>
  )
}
