import { onBeforeUnmount } from 'vue'

export function useDraggableGroupTabs(groups, moveGroup, options = {}) {
  const selector = options.selector ?? '.n-tabs-tab'
  const documentRef = options.documentRef ?? (typeof document === 'undefined' ? null : document)
  const setDelay = options.setDelay ?? setTimeout
  let sourceID = null
  let targetID = null
  let pendingTimer

  const groupID = (event) => Number(event.currentTarget?.getAttribute('data-name'))

  function dragStart(event) {
    const id = groupID(event)
    if (id <= 0) {
      event.preventDefault()
      return
    }
    sourceID = id
    event.dataTransfer.effectAllowed = 'move'
    event.currentTarget?.classList.add('tab-dragging')
  }

  function dragOver(event) {
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'
  }

  function dragEnter(event) {
    event.preventDefault()
    const id = groupID(event)
    if (id > 0) {
      targetID = id
      event.currentTarget?.classList.add('tab-drag-over')
    }
  }

  function dragLeave(event) {
    event.currentTarget?.classList.remove('tab-drag-over')
  }

  function resetClasses() {
    documentRef?.querySelectorAll(selector).forEach((tab) => {
      tab.classList.remove('tab-drag-over', 'tab-dragging')
    })
  }

  async function drop(event) {
    event.preventDefault()
    resetClasses()
    if (sourceID > 0 && targetID > 0 && sourceID !== targetID) {
      const source = groups.value.find((group) => group.ID === sourceID)
      const target = groups.value.find((group) => group.ID === targetID)
      if (source && target) await moveGroup(source, target.sort)
    }
    sourceID = null
    targetID = null
  }

  function dragEnd() {
    resetClasses()
    sourceID = null
    targetID = null
  }

  function cleanup() {
    if (pendingTimer !== undefined) clearTimeout(pendingTimer)
    pendingTimer = undefined
    documentRef?.querySelectorAll(selector).forEach((tab) => {
      tab.removeEventListener('dragstart', dragStart)
      tab.removeEventListener('dragover', dragOver)
      tab.removeEventListener('dragenter', dragEnter)
      tab.removeEventListener('dragleave', dragLeave)
      tab.removeEventListener('drop', drop)
      tab.removeEventListener('dragend', dragEnd)
      tab.removeAttribute('draggable')
    })
  }

  function initialize() {
    cleanup()
    pendingTimer = setDelay(() => {
      documentRef?.querySelectorAll(selector).forEach((tab) => {
        if (Number(tab.getAttribute('data-name')) <= 0) return
        tab.setAttribute('draggable', 'true')
        tab.addEventListener('dragstart', dragStart)
        tab.addEventListener('dragover', dragOver)
        tab.addEventListener('dragenter', dragEnter)
        tab.addEventListener('dragleave', dragLeave)
        tab.addEventListener('drop', drop)
        tab.addEventListener('dragend', dragEnd)
      })
    }, 100)
  }

  onBeforeUnmount(cleanup)
  return { initialize, cleanup }
}
