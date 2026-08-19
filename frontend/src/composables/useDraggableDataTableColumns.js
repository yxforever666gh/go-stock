import {h, ref} from 'vue'

function safeLoadSavedOrder(storageKey, allowedKeys) {
  try {
    const raw = window.localStorage.getItem(storageKey)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    const seen = new Set()
    return parsed.map(item => String(item)).filter(key => {
      if (!allowedKeys.has(key) || seen.has(key)) return false
      seen.add(key)
      return true
    })
  } catch (error) {
    console.warn(`读取表格列顺序失败: ${storageKey}`, error)
    return []
  }
}

export function useDraggableDataTableColumns(defaultColumns, storageKey) {
  const tableRef = ref(null)
  const dragSourceKeyRef = ref('')
  const columnsRef = ref(buildInitialColumns())

  function tableRoot() {
    return tableRef.value?.$el || tableRef.value
  }

  function clearDragStyles() {
    tableRoot()?.querySelectorAll('.draggable-column-title').forEach(title => {
      title.classList.remove('column-drag-over', 'column-dragging')
    })
  }

  function persistColumnOrder() {
    try {
      window.localStorage.setItem(storageKey, JSON.stringify(columnsRef.value.map(column => String(column.key))))
    } catch (error) {
      console.warn(`保存表格列顺序失败: ${storageKey}`, error)
    }
  }

  function moveColumn(sourceKey, targetKey) {
    const sourceIndex = columnsRef.value.findIndex(column => String(column.key) === String(sourceKey))
    const targetIndex = columnsRef.value.findIndex(column => String(column.key) === String(targetKey))
    if (sourceIndex === -1 || targetIndex === -1 || sourceIndex === targetIndex) return

    const nextColumns = [...columnsRef.value]
    const [movedColumn] = nextColumns.splice(sourceIndex, 1)
    nextColumns.splice(targetIndex, 0, movedColumn)
    columnsRef.value = nextColumns
    persistColumnOrder()
  }

  function draggableTitle(label, key) {
    return () => h('span', {
      class: 'draggable-column-title',
      'data-column-key': key,
      draggable: true,
      onDragstart: event => {
        dragSourceKeyRef.value = String(key)
        event.dataTransfer.effectAllowed = 'move'
        event.dataTransfer.setData('text/plain', String(key))
        event.currentTarget.classList.add('column-dragging')
      },
      onDragover: event => {
        event.preventDefault()
        event.dataTransfer.dropEffect = 'move'
      },
      onDragenter: event => {
        event.preventDefault()
        if (dragSourceKeyRef.value && dragSourceKeyRef.value !== String(key)) {
          event.currentTarget.classList.add('column-drag-over')
        }
      },
      onDragleave: event => event.currentTarget.classList.remove('column-drag-over'),
      onDrop: event => {
        event.preventDefault()
        const sourceKey = dragSourceKeyRef.value || event.dataTransfer.getData('text/plain')
        moveColumn(sourceKey, key)
        dragSourceKeyRef.value = ''
        clearDragStyles()
      },
      onDragend: () => {
        dragSourceKeyRef.value = ''
        clearDragStyles()
      },
    }, label)
  }

  function decorateColumns(columns) {
    return columns.map(column => {
      const label = String(column.title ?? column.key ?? '')
      return {...column, title: draggableTitle(label, column.key), __dragLabel: label}
    })
  }

  function buildInitialColumns() {
    const allowedKeys = new Set(defaultColumns.map(column => String(column.key)))
    const savedOrder = safeLoadSavedOrder(storageKey, allowedKeys)
    if (savedOrder.length === 0) return decorateColumns(defaultColumns)

    const columnMap = new Map(defaultColumns.map(column => [String(column.key), column]))
    const orderedColumns = savedOrder.map(key => columnMap.get(key)).filter(Boolean)
    const missingColumns = defaultColumns.filter(column => !savedOrder.includes(String(column.key)))
    return decorateColumns([...orderedColumns, ...missingColumns])
  }

  return {tableRef, columnsRef}
}
