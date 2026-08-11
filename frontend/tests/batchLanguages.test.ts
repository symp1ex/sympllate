import assert from 'node:assert/strict'
import test from 'node:test'
import { defaultConcreteSource, sourceLanguageForImage } from '../src/languageDefaults.ts'

const languages = [
  { code: 'auto', name: 'Auto-detect' },
  { code: 'ru', name: 'Russian' },
  { code: 'en', name: 'English' },
]

test('uses the configured second language as the batch source', () => {
  assert.equal(defaultConcreteSource(languages, 'en'), 'en')
})

test('falls back to the first concrete language', () => {
  assert.equal(defaultConcreteSource(languages, ''), 'ru')
  assert.equal(defaultConcreteSource(languages, 'missing'), 'ru')
  assert.equal(defaultConcreteSource([{ code: 'auto', name: 'Auto-detect' }, { code: '', name: 'Invalid' }, ...languages.slice(1)], 'missing'), 'ru')
})

test('returns an empty safe value when no concrete language is available', () => {
  assert.equal(defaultConcreteSource([], 'en'), '')
  assert.equal(defaultConcreteSource([{ code: 'auto', name: 'Auto-detect' }], 'en'), '')
})

test('image mode replaces only auto-detect with a concrete source', () => {
  assert.equal(sourceLanguageForImage('auto', languages, 'en'), 'en')
  assert.equal(sourceLanguageForImage('auto', languages, 'missing'), 'ru')
  assert.equal(sourceLanguageForImage('ru', languages, 'en'), 'ru')
})

test('image mode keeps auto-detect when it is the only available source', () => {
  assert.equal(sourceLanguageForImage('auto', [{ code: 'auto', name: 'Auto-detect' }], 'en'), 'auto')
})
