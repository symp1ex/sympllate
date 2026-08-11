import type { Language } from './api'

export function defaultConcreteSource(languages: Language[], configuredSource: string): string {
  const realLanguages = languages.filter((language) => language.code !== 'auto' && language.code.trim() !== '')
  if (configuredSource && realLanguages.some((language) => language.code === configuredSource)) return configuredSource
  return realLanguages[0]?.code ?? ''
}

export function sourceLanguageForImage(currentSource: string, languages: Language[], configuredSource: string): string {
  if (currentSource !== 'auto') return currentSource
  return defaultConcreteSource(languages, configuredSource) || currentSource
}
