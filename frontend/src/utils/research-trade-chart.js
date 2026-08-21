function tradingDate(value) {
  return String(value || '').slice(0, 10)
}

export function tradingDaySeparatorIndexes(categories) {
  const separators = new Set()
  for (let index = 1; index < categories.length; index += 1) {
    const currentDate = tradingDate(categories[index])
    const previousDate = tradingDate(categories[index - 1])
    if (currentDate && previousDate && currentDate !== previousDate) {
      separators.add(index)
    }
  }
  return separators
}
