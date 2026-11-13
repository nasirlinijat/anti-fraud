import type { ModelMode, ModelSettings } from '../api'

export interface ModelDraft {
  mode: ModelMode
  clear_below: string
  flag_at: string
}

export type FieldErrors = Record<string, string>
export type ModelThreshold = 'clear_below' | 'flag_at'

export function toModelDraft(model: ModelSettings): ModelDraft {
  return {
    mode: model.mode,
    clear_below: model.clear_below?.toString() ?? '',
    flag_at: model.flag_at?.toString() ?? '',
  }
}

export function toModelConfig(draft: ModelDraft, errors: FieldErrors): ModelSettings {
  const clearBelow = parseThreshold(draft.clear_below, 'clear_below', errors)
  const flagAt = parseThreshold(draft.flag_at, 'flag_at', errors)
  if (clearBelow !== null && flagAt !== null && clearBelow >= flagAt) {
    errors['model.clear_below'] = 'Enter a value lower than the flag threshold.'
  } else if (draft.clear_below.trim() && !draft.flag_at.trim()) {
    errors['model.flag_at'] = 'Enter a flag threshold or leave both thresholds blank.'
  } else if (draft.flag_at.trim() && !draft.clear_below.trim()) {
    errors['model.clear_below'] = 'Enter a clear threshold or leave both thresholds blank.'
  }
  return { mode: draft.mode, clear_below: clearBelow, flag_at: flagAt }
}

function parseThreshold(value: string, field: ModelThreshold, errors: FieldErrors): number | null {
  const text = value.trim()
  if (!text) return null
  const number = Number(text)
  if (!/^(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$/i.test(text) || number < 0 || number > 1) {
    errors[`model.${field}`] = 'Enter a number from 0 to 1.'
    return null
  }
  return number
}
