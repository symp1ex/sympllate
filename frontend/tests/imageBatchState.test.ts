import assert from 'node:assert/strict'
import test from 'node:test'
import { imageBatchProgress, imageBatchStateLabel, isTerminalImageBatchState } from '../src/imageBatchState.ts'

test('batch progress is bounded and deterministic', () => {
  assert.equal(imageBatchProgress({ processed: 42, total: 125 }), 34)
  assert.equal(imageBatchProgress({ processed: 2, total: 0 }), 0)
  assert.equal(imageBatchProgress({ processed: 12, total: 10 }), 100)
})

test('only final batch states are terminal', () => {
  assert.equal(isTerminalImageBatchState('processing'), false)
  for (const state of ['completed', 'completed_with_errors', 'cancelled', 'failed'] as const) assert.equal(isTerminalImageBatchState(state), true)
})

test('all batch states have user-facing labels', () => {
  assert.equal(imageBatchStateLabel('completed_with_errors'), 'Completed with errors')
  assert.equal(imageBatchStateLabel('failed'), 'Processing failed')
})
