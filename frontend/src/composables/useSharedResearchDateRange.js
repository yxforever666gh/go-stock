import { computed, ref } from 'vue'
import { GetAiRecommendStocksDateRange } from '../services/app-api'

const STORAGE_KEY = 'shared-research-date-range'
const DEFAULT_START_DATE = new Date(2026, 1, 1)

const sharedRangeRef = ref([
  new Date(DEFAULT_START_DATE.getTime()),
  new Date()
])
const initializedRef = ref(false)
let initPromise = null

function cloneDate(date) {
  return new Date(date.getTime())
}

function todayDateOnly() {
  const today = new Date()
  return new Date(today.getFullYear(), today.getMonth(), today.getDate())
}

function parseDateOnly(dateStr) {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(dateStr || ''))
  if (!match) {
    return null
  }
  return new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
}

function normalizeDate(value) {
  if (value instanceof Date) {
    if (Number.isNaN(value.getTime())) {
      return null
    }
    return new Date(value.getFullYear(), value.getMonth(), value.getDate())
  }

  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return null
  }
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

function normalizeRange(range) {
  if (!Array.isArray(range) || range.length !== 2) {
    return null
  }

  const start = normalizeDate(range[0])
  const end = normalizeDate(range[1])
  if (!start || !end) {
    return null
  }

  if (start.getTime() <= end.getTime()) {
    return [start, end]
  }
  return [end, start]
}

function formatStorageDate(date) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function serializeRange(range) {
  return range.map((date) => formatStorageDate(date))
}

function rangeKey(range) {
  return serializeRange(range).join('|')
}

function resolveLatestEndDate(candidate) {
  const today = todayDateOnly()
  const normalizedCandidate = normalizeDate(candidate)
  if (!normalizedCandidate) {
    return today
  }
  if (normalizedCandidate.getTime() >= today.getTime()) {
    return normalizedCandidate
  }
  return today
}

function alignRangeEndToLatest(range, latestEndDate) {
  const normalized = normalizeRange(range)
  const normalizedLatestEnd = normalizeDate(latestEndDate)
  if (!normalized || !normalizedLatestEnd) {
    return null
  }
  const start = cloneDate(normalized[0])
  const end = cloneDate(normalizedLatestEnd)
  if (start.getTime() > end.getTime()) {
    return [cloneDate(end), cloneDate(end)]
  }
  return [start, end]
}

function persistRange(range) {
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(serializeRange(range)))
}

function loadSavedRange() {
  const raw = window.localStorage.getItem(STORAGE_KEY)
  if (!raw) {
    return null
  }

  try {
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) {
      return null
    }
    return normalizeRange(parsed)
  } catch (error) {
    console.warn('解析共享研究时间窗口失败:', error)
    return null
  }
}

function applyRange(range) {
  const normalized = normalizeRange(range)
  if (!normalized) {
    return
  }

  if (rangeKey(sharedRangeRef.value) === rangeKey(normalized)) {
    return
  }

  sharedRangeRef.value = normalized
  persistRange(normalized)
}

async function resolveInitialRange() {
  let latestDate = todayDateOnly()
  try {
    const result = await GetAiRecommendStocksDateRange()
    const end = parseDateOnly(result?.endDate)
    if (end) {
      latestDate = resolveLatestEndDate(end)
    }
  } catch (error) {
    console.error('初始化共享研究时间窗口失败', error)
  }

  const savedRange = loadSavedRange()
  if (savedRange) {
    return alignRangeEndToLatest(savedRange, latestDate) || savedRange
  }

  if (latestDate.getTime() < DEFAULT_START_DATE.getTime()) {
    latestDate = cloneDate(DEFAULT_START_DATE)
  }

  return [cloneDate(DEFAULT_START_DATE), normalizeDate(latestDate)]
}

async function initSharedResearchDateRange() {
  if (initializedRef.value) {
    return sharedRangeRef.value
  }

  if (!initPromise) {
    initPromise = resolveInitialRange()
      .then((range) => {
        const normalized = normalizeRange(range)
        if (normalized) {
          sharedRangeRef.value = normalized
          persistRange(normalized)
        }
        initializedRef.value = true
        return sharedRangeRef.value
      })
      .finally(() => {
        initPromise = null
      })
  }

  return initPromise
}

export function useSharedResearchDateRange() {
  return {
    researchDateRangeModel: computed({
      get: () => sharedRangeRef.value,
      set: (nextValue) => {
        applyRange(nextValue)
      }
    }),
    researchDateRangeKey: computed(() => rangeKey(sharedRangeRef.value)),
    initSharedResearchDateRange,
    setSharedResearchDateRange: applyRange,
    sharedResearchDateRangeReady: computed(() => initializedRef.value)
  }
}
