export interface Language { code: string; name: string }
export interface LanguagePair { first: string; second: string }
export interface ClientConfig { defaultLanguagePair: LanguagePair; fallbackTargetLanguage: string; maxInputCharacters: number }
export interface TranslateRequest { text: string; source: string; target: string }
export interface TranslateResult { text: string; detectedLanguage?: string }
export interface JobStatus { state: 'pending' | 'done' | 'error'; result?: TranslateResult; error?: string }
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

declare global {
  interface Window {
    Translate(request: TranslateRequest): Promise<string>
    GetTranslation(id: string): Promise<JobStatus>
    GetConfig(): Promise<ClientConfig>
    GetSupportedLanguages(): Promise<Language[]>
    GetWindowMode(): Promise<'main' | 'popup'>
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
  }
}

const wait = (milliseconds: number) => new Promise<void>((resolve) => window.setTimeout(resolve, milliseconds))

export async function translate(request: TranslateRequest): Promise<TranslateResult> {
  const id = await window.Translate(request)
  for (;;) {
    const status = await window.GetTranslation(id)
    if (status.state === 'done' && status.result) return status.result
    if (status.state === 'error') throw new Error(status.error ?? 'Не удалось выполнить перевод')
    await wait(80)
  }
}

export function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return typeof error === 'string' ? error : 'Неизвестная ошибка'
}
