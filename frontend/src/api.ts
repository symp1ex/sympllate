export interface Language { code: string; name: string }
export interface LanguagePair { first: string; second: string }
export interface ClientConfig {
  defaultLanguagePair: LanguagePair
  fallbackTargetLanguage: string
  maxInputCharacters: number
  maxImageBytes: number
  maxImageBase64Characters: number
}
export interface TranslateRequest { text: string; source: string; target: string }
export interface TranslateResult { text: string; detectedLanguage?: string }
export interface JobStatus { state: 'pending' | 'done' | 'error'; result?: TranslateResult; error?: string }
export interface ImageTranslateRequest { dataBase64: string; mediaType: string; source: string; target: string }
export interface ImageTranslateResult { text: string; detectedLanguage?: string }
export interface ImageJobStatus { state: 'pending' | 'done' | 'error'; result?: ImageTranslateResult; error?: string }
export type BatchSelectionKind = 'files' | 'directory'
export interface BatchSelection { id: string; kind: BatchSelectionKind; displayName: string; fileCount: number }
export interface StartImageBatchRequest { selectionId: string; source: string; target: string; debug: boolean; fillWordBoxes: boolean }
export type ImageBatchState = 'pending' | 'preparing' | 'processing' | 'completed' | 'completed_with_errors' | 'cancelled' | 'failed'
export type ImageBatchStage = 'prepare_render' | 'ocr' | 'translate' | 'layout_text' | 'clean_background' | 'render_text' | 'encode_output' | 'verify_output'
export interface ImageBatchStatus {
  id: string
  state: ImageBatchState
  total: number
  processed: number
  translated: number
  rendered: number
  partial: number
  warnings: number
  noText: number
  failed: number
  currentFile?: string
  currentStage?: ImageBatchStage
  outputDirectory?: string
  error?: string
}
export interface PopupState {
  source: string
  target: string
  detectedLanguage?: string
  translatedText?: string
  loading: boolean
  error?: string
}
export type JsonSettingValue = string | number | boolean | null | JsonSettingObject | JsonSettingValue[]
export interface JsonSettingObject { [key: string]: JsonSettingValue }
export interface SelectSetting extends JsonSettingObject { active: string; list: string[] }
export type ApplicationInfo = { version: string; updaterEnabled: boolean }
export type CheckApplicationUpdateResult = { ok: boolean; updateAvailable: boolean; message?: string }
export type InstallApplicationUpdateResult = { ok: boolean; message?: string }

declare global {
  interface Window {
    Translate(request: TranslateRequest): Promise<string>
    GetTranslation(id: string): Promise<JobStatus>
    TranslateImage(request: ImageTranslateRequest): Promise<string>
    GetImageTranslation(id: string): Promise<ImageJobStatus>
    SelectBatchImageFiles(): Promise<BatchSelection>
    SelectBatchImageDirectory(): Promise<BatchSelection>
    StartImageBatch(request: StartImageBatchRequest): Promise<string>
    GetImageBatchStatus(id: string): Promise<ImageBatchStatus>
    CancelImageBatch(id: string): Promise<void>
    GetConfig(): Promise<ClientConfig>
    GetSupportedLanguages(): Promise<Language[]>
    GetWindowMode(): Promise<'main' | 'popup' | 'batch'>
    OpenImageBatchWindow(): Promise<void>
    GetInitialView(): Promise<'main' | 'settings'>
    GetSettingsConfig(): Promise<JsonSettingObject>
    SaveSettingsConfig(config: JsonSettingObject): Promise<void>
    WindowMinimize(): Promise<void>
    WindowToggleMaximize(): Promise<boolean>
    WindowClose(): Promise<void>
    WindowDrag(): Promise<void>
    WindowResize(hitTest: number): Promise<void>
    CopyText(text: string): Promise<void>
    HidePopup(): Promise<void>
    SetQuickTranslationTarget(target: string): Promise<void>
    GetPopupState(): Promise<PopupState>
    getApplicationInfo?: () => ApplicationInfo | Promise<ApplicationInfo>
    checkApplicationUpdate?: () => CheckApplicationUpdateResult | Promise<CheckApplicationUpdateResult>
    installApplicationUpdate?: () => InstallApplicationUpdateResult | Promise<InstallApplicationUpdateResult>
  }
}

const wait = (milliseconds: number) => new Promise<void>((resolve) => window.setTimeout(resolve, milliseconds))

export async function translate(request: TranslateRequest): Promise<TranslateResult> {
  const id = await window.Translate(request)
  for (;;) {
    const status = await window.GetTranslation(id)
    if (status.state === 'done' && status.result) return status.result
    if (status.state === 'error') throw new Error(status.error ?? 'Failed to translate')
    await wait(80)
  }
}

export async function translateImage(request: ImageTranslateRequest): Promise<ImageTranslateResult> {
  const id = await window.TranslateImage(request)
  for (;;) {
    const status = await window.GetImageTranslation(id)
    if (status.state === 'done') {
      if (status.result) return status.result
      throw new Error('Image translation completed without a result')
    }
    if (status.state === 'error') throw new Error(status.error ?? 'Failed to translate image')
    await wait(80)
  }
}

export function selectBatchImageFiles(): Promise<BatchSelection> { return window.SelectBatchImageFiles() }
export function selectBatchImageDirectory(): Promise<BatchSelection> { return window.SelectBatchImageDirectory() }
export function startImageBatch(request: StartImageBatchRequest): Promise<string> { return window.StartImageBatch(request) }
export function cancelImageBatch(id: string): Promise<void> { return window.CancelImageBatch(id) }
export function openImageBatchWindow(): Promise<void> { return window.OpenImageBatchWindow() }

export async function pollImageBatch(
  id: string,
  onStatus: (status: ImageBatchStatus) => void,
  signal: AbortSignal,
  intervalMilliseconds = 350,
): Promise<ImageBatchStatus> {
  for (;;) {
    if (signal.aborted) throw new Error('Image batch polling cancelled')
    const status = await window.GetImageBatchStatus(id)
    if (signal.aborted) throw new Error('Image batch polling cancelled')
    onStatus(status)
    if (status.state === 'completed' || status.state === 'completed_with_errors' || status.state === 'cancelled' || status.state === 'failed') return status
    await wait(intervalMilliseconds)
  }
}

export function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return typeof error === 'string' ? error : 'Unknown error'
}
