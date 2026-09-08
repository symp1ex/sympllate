import assert from 'node:assert/strict'
import test from 'node:test'
import { defaultConcreteSource, nonCollidingTarget } from '../src/languageDefaults.ts'

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

test('keeps a target that differs from the selected source', () => {
  assert.equal(nonCollidingTarget('ru', 'fr', 'ru', 'en'), 'fr')
})

test('selects the other default language when source and target collide', () => {
  assert.equal(nonCollidingTarget('ru', 'ru', 'ru', 'en'), 'en')
  assert.equal(nonCollidingTarget('en', 'en', 'ru', 'en'), 'ru')
  assert.equal(nonCollidingTarget('fr', 'fr', 'ru', 'en'), 'en')
})

test('does not treat auto-detect as a concrete source', () => {
  assert.equal(nonCollidingTarget('auto', 'ru', 'ru', 'en'), 'ru')
})
