const SHANGHAI_FORMATTER = new Intl.DateTimeFormat('en-US', {
  timeZone: 'Asia/Shanghai',
  weekday: 'short',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})

function shanghaiParts(now) {
  return Object.fromEntries(SHANGHAI_FORMATTER.formatToParts(now).map(part => [part.type, part.value]))
}

export function isChinaTradingSession(now = new Date()) {
  const parts = shanghaiParts(now)
  if (parts.weekday === 'Sat' || parts.weekday === 'Sun') return false
  const minutes = Number(parts.hour) * 60 + Number(parts.minute)
  return (minutes >= 9 * 60 + 15 && minutes <= 11 * 60 + 30)
    || (minutes >= 13 * 60 && minutes <= 15 * 60)
}

export function shanghaiDate(now = new Date()) {
  const parts = shanghaiParts(now)
  return `${parts.year}-${parts.month}-${parts.day}`
}

export function isMarketSessionOpen(session, now = new Date()) {
  if (typeof session === 'function') return session(now)
  if (session === 'always') return true
  return isChinaTradingSession(now)
}
