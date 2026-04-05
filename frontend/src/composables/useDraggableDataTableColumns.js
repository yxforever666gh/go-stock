import { h, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

function createDraggableTitle(label, key) {
  return () => h('span', {
    class: 'draggable-column-title',
    'data-column-key': key,
  }, label)
}

function decorateColumns(columns) {
  return columns.map((column) => {
    const label = String(column.title ?? column.key ?? '')
    return {
      ...column,
      title: createDraggableTitle(label, column.key),
      __dragLabel: label,
    }
  })
}

function safeLoadSavedOrder(storageKey) {
  const raw = window.localStorage.getItem(storageKey)
  if (!raw) {
    return []
  }
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.map((item) => String(item)) : []
  } catch (error) {
    console.warn(`解析表格列顺序失败: ${storageKey}`, error)
    return []
  }
}

export function useDraggableDataTableColumns(defaultColumns, storageKey) {
  const tableRef = ref(null)
  const columnsRef = ref(buildInitialColumns())
  const dragSourceKeyRef = ref('')

  function buildInitialColumns() {
    const savedOrder = safeLoadSavedOrder(storageKey)
    if (savedOrder.length === 0) {
      return decorateColumns(defaultColumns)
    }

    const columnMap = new Map(defaultColumns.map((column) => [String(column.key), column]))
    const orderedColumns = savedOrder
        .map((key) => columnMap.get(key))
        .filter(Boolean)
    const missingColumns = defaultColumns.filter((column) => !savedOrder.includes(String(column.key)))
    return decorateColumns([...orderedColumns, ...missingColumns])
  }

  function persistColumnOrder() {
    window.localStorage.setItem(
        storageKey,
        JSON.stringify(columnsRef.value.map((column) => String(column.key)))
    )
  }

  function clearDragStyles() {
    const root = tableRef.value?.$el
    if (!root) {
      return
    }
    root.querySelectorAll('.n-data-table-th').forEach((headerCell) => {
      headerCell.classList.remove('column-drag-over', 'column-dragging')
    })
  }

  function moveColumn(sourceKey, targetKey) {
    const sourceIndex = columnsRef.value.findIndex((column) => String(column.key) === String(sourceKey))
    const targetIndex = columnsRef.value.findIndex((column) => String(column.key) === String(targetKey))
    if (sourceIndex === -1 || targetIndex === -1 || sourceIndex === targetIndex) {
      return
    }

    const nextColumns = [...columnsRef.value]
    const [movedColumn] = nextColumns.splice(sourceIndex, 1)
    nextColumns.splice(targetIndex, 0, movedColumn)
    columnsRef.value = nextColumns
    persistColumnOrder()
  }

  function cleanupDraggableHeaders() {
    const root = tableRef.value?.$el
    if (!root) {
      return
    }
    root.querySelectorAll('.draggable-column-title[data-column-key]').forEach((titleEl) => {
      const handlers = titleEl.__dragColumnHandlers
      if (handlers) {
        titleEl.removeEventListener('dragstart', handlers.dragstart)
        titleEl.removeEventListener('dragover', handlers.dragover)
        titleEl.removeEventListener('dragenter', handlers.dragenter)
        titleEl.removeEventListener('dragleave', handlers.dragleave)
        titleEl.removeEventListener('drop', handlers.drop)
        titleEl.removeEventListener('dragend', handlers.dragend)
        delete titleEl.__dragColumnHandlers
      }
      titleEl.removeAttribute('draggable')
    })
    clearDragStyles()
  }

  function initDraggableHeaders() {
    cleanupDraggableHeaders()
    nextTick(() => {
      const root = tableRef.value?.$el
      if (!root) {
        return
      }
      root.querySelectorAll('.draggable-column-title[data-column-key]').forEach((titleEl) => {
        const key = String(titleEl.getAttribute('data-column-key') || '')
        if (!key) {
          return
        }

        const handlers = {
          dragstart: (event) => {
            dragSourceKeyRef.value = key
            event.dataTransfer.effectAllowed = 'move'
            event.dataTransfer.setData('text/plain', key)
            titleEl.closest('.n-data-table-th')?.classList?.add('column-dragging')
          },
          dragover: (event) => {
            event.preventDefault()
            event.dataTransfer.dropEffect = 'move'
          },
          dragenter: (event) => {
            event.preventDefault()
            if (dragSourceKeyRef.value && dragSourceKeyRef.value !== key) {
              titleEl.closest('.n-data-table-th')?.classList?.add('column-drag-over')
            }
          },
          dragleave: () => {
            titleEl.closest('.n-data-table-th')?.classList?.remove('column-drag-over')
          },
          drop: (event) => {
            event.preventDefault()
            const sourceKey = dragSourceKeyRef.value || event.dataTransfer.getData('text/plain')
            moveColumn(sourceKey, key)
            dragSourceKeyRef.value = ''
            clearDragStyles()
          },
          dragend: () => {
            dragSourceKeyRef.value = ''
            clearDragStyles()
          },
        }

        titleEl.__dragColumnHandlers = handlers
        titleEl.setAttribute('draggable', 'true')
        titleEl.addEventListener('dragstart', handlers.dragstart)
        titleEl.addEventListener('dragover', handlers.dragover)
        titleEl.addEventListener('dragenter', handlers.dragenter)
        titleEl.addEventListener('dragleave', handlers.dragleave)
        titleEl.addEventListener('drop', handlers.drop)
        titleEl.addEventListener('dragend', handlers.dragend)
      })
    })
  }

  onMounted(() => {
    initDraggableHeaders()
  })

  watch(() => columnsRef.value.map((column) => String(column.key)).join('|'), () => {
    initDraggableHeaders()
  })

  onBeforeUnmount(() => {
    cleanupDraggableHeaders()
  })

  return {
    tableRef,
    columnsRef,
  }
}
