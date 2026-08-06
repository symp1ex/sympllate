import type { ImageBatchState, ImageBatchStatus } from './api'

export function isTerminalImageBatchState(state: ImageBatchState): boolean {
  return state === 'completed' || state === 'completed_with_errors' || state === 'cancelled' || state === 'failed'
}

export function imageBatchProgress(status: Pick<ImageBatchStatus, 'processed' | 'total'>): number {
  if (status.total <= 0) return 0
  return Math.min(100, Math.max(0, Math.round(status.processed * 100 / status.total)))
}

export function imageBatchStateLabel(state: ImageBatchState): string {
  switch (state) {
    case 'pending': return 'Waiting to start'
    case 'preparing': return 'Preparing output'
    case 'processing': return 'Processing images'
    case 'completed': return 'Processing completed'
    case 'completed_with_errors': return 'Completed with errors'
    case 'cancelled': return 'Processing cancelled'
    case 'failed': return 'Processing failed'
  }
}
