export function formatGitHubVersionLabel(version) {
  const normalized = String(version || '').trim()
  return `GitHub · v${normalized || 'dev'}`
}
