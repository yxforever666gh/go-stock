import {computed, ref, shallowRef, unref, watch} from 'vue'
import {
  createDrawing,
  drawingPutPayload,
  isDrawingRevisionConflict,
  normalizeDrawingDocument,
  softDeleteDrawing,
} from '../charting/drawing-model.js'
import {drawingScopeKey} from '../charting/chart-contract.js'
import {
  DeleteInstrumentDrawings,
  GetInstrumentDrawings,
  PutInstrumentDrawings,
} from '../services/instruments-api.js'

export function useChartDrawings(scopeRef) {
  const document = shallowRef(normalizeDrawingDocument({}, unref(scopeRef)))
  const loading = ref(false)
  const saving = ref(false)
  const error = ref('')
  const conflict = ref('')
  const unsavedDrawings = shallowRef([])
  let requestVersion = 0

  const scope = computed(() => unref(scopeRef) || {})
  const scopeKey = computed(() => drawingScopeKey(scope.value))

  function apiScope(expectedRevision) {
    return {
      assetType: document.value.instrument.assetType,
      market: document.value.instrument.market,
      period: document.value.period,
      adjustment: document.value.adjustment,
      expectedRevision,
    }
  }

  async function load() {
    const target = scope.value
    if (!target.instrument?.code) return
    const version = ++requestVersion
    loading.value = true
    error.value = ''
    try {
      const result = await GetInstrumentDrawings(target.instrument.code, {
        assetType: target.instrument.assetType,
        market: target.instrument.market,
        period: target.period,
        adjustment: target.adjustment,
      })
      if (version !== requestVersion) return
      document.value = normalizeDrawingDocument(result?.data || result, target)
    } catch (reason) {
      if (version !== requestVersion) return
      error.value = reason?.message || String(reason)
      document.value = normalizeDrawingDocument({}, target)
    } finally {
      if (version === requestVersion) loading.value = false
    }
  }

  async function save(nextDrawings) {
    if (!document.value.instrument.code || saving.value) return document.value
    const currentScope = scopeKey.value
    const expectedRevision = document.value.revision
    saving.value = true
    error.value = ''
    conflict.value = ''
    unsavedDrawings.value = nextDrawings
    try {
      const result = await PutInstrumentDrawings(
        document.value.instrument.code,
        drawingPutPayload(document.value, nextDrawings),
      )
      if (currentScope !== scopeKey.value) return document.value
      document.value = normalizeDrawingDocument(result?.data || result, scope.value)
      unsavedDrawings.value = []
      return document.value
    } catch (reason) {
      if (currentScope !== scopeKey.value) return document.value
      error.value = reason?.message || String(reason)
      if (isDrawingRevisionConflict(reason)) {
        conflict.value = '绘图已被其他页面修改，已加载服务器最新版；未提交草稿不会自动覆盖。'
        await load()
      }
      throw reason
    } finally {
      if (currentScope === scopeKey.value) saving.value = false
    }
  }

  async function add(type, points, options) {
    return save([...document.value.drawings, createDrawing(type, points, options)])
  }

  async function remove(id) {
    const next = document.value.drawings.map(item => item.id === id ? softDeleteDrawing(item) : item)
    return save(next)
  }

  async function clear() {
    if (!document.value.instrument.code || saving.value) return document.value
    const currentScope = scopeKey.value
    const expectedRevision = document.value.revision
    saving.value = true
    error.value = ''
    conflict.value = ''
    try {
      const result = await DeleteInstrumentDrawings(
        document.value.instrument.code,
        apiScope(expectedRevision),
      )
      if (currentScope !== scopeKey.value) return document.value
      document.value = normalizeDrawingDocument(result?.data || result, scope.value)
      return document.value
    } catch (reason) {
      if (currentScope !== scopeKey.value) return document.value
      error.value = reason?.message || String(reason)
      if (isDrawingRevisionConflict(reason)) {
        conflict.value = '清空失败：绘图版本已经变化，已加载服务器最新版。'
        await load()
      }
      throw reason
    } finally {
      if (currentScope === scopeKey.value) saving.value = false
    }
  }

  watch(scopeKey, () => {
    requestVersion += 1
    conflict.value = ''
    unsavedDrawings.value = []
    document.value = normalizeDrawingDocument({}, scope.value)
    void load()
  }, {immediate: true})

  return {document, loading, saving, error, conflict, unsavedDrawings, load, save, add, remove, clear}
}
