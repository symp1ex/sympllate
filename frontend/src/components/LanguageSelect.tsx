import type { Language } from '../api'

interface Props { id: string; label: string; value: string; languages: Language[]; onChange(value: string): void; disabled?: boolean; allowAuto?: boolean }

export function LanguageSelect({ id, label, value, languages, onChange, disabled, allowAuto = true }: Props) {
  return (
    <label className="language-select" htmlFor={id}>
      <span>{label}</span>
      <select id={id} value={value} onChange={(event) => onChange(event.target.value)} disabled={disabled}>
        {languages.filter((language) => allowAuto || language.code !== 'auto').map((language) => (
          <option value={language.code} key={language.code}>{language.name}</option>
        ))}
      </select>
    </label>
  )
}

