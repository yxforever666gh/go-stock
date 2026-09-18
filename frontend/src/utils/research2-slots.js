export const RESEARCH2_SLOTS = Array.from({length: 24}, (_, index) => {
  const minute = 570 + index * 5
  const clock = value => `${String(Math.floor(value / 60)).padStart(2, '0')}:${String(value % 60).padStart(2, '0')}`
  return {value: clock(minute), label: `${clock(minute)}–${clock(minute + 5)}`}
})

export const validResearch2Slot = value => RESEARCH2_SLOTS.some(slot => slot.value === value)
