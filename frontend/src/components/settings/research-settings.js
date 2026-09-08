export function researchPayload(snapshot, values, aiConfigs) {
  return {
    revision: snapshot.revision,
    config: Object.fromEntries(Object.keys(snapshot.config).map(key => [key, values[key] ?? snapshot.config[key]])),
    aiConfigs: JSON.parse(JSON.stringify(aiConfigs)),
  }
}

// A request can complete after rows were edited, removed or reordered. Match
// the submitted object identity, not the row's current index, when accepting IDs.
export function acceptSavedModelIDs(currentRows, submittedRows, submittedModels, savedModels) {
  for (let i = 0; i < submittedRows.length; i++) {
    const row = submittedRows[i]
    if (!currentRows.includes(row)) continue
    const saved = submittedModels[i].ID
      ? savedModels.find(item => item.ID === submittedModels[i].ID)
      : savedModels[i]
    if (saved) {
      row.ID = saved.ID
      row.CreatedAt = saved.CreatedAt
      row.UpdatedAt = saved.UpdatedAt
    }
  }
  currentRows.forEach((row, index) => { row.sort = index + 1 })
}

export function importResearchSettings(snapshot, imported, center) {
  if (!imported || typeof imported !== 'object' || Array.isArray(imported)) throw new Error('配置必须是 JSON 对象')
  if (imported.center && imported.center !== center) throw new Error('请选择当前研究中心导出的配置')
  const values = imported.config || imported
  const models = imported.aiConfigs === undefined ? snapshot.aiConfigs : imported.aiConfigs
  if (!Array.isArray(models) || models.some(model => !model || typeof model !== 'object' || Array.isArray(model))) throw new Error('模型列表无效')
  const result = researchPayload(snapshot, values, models)
  if (imported.aiConfigs !== undefined) {
    result.aiConfigs = result.aiConfigs.map(model => {
      const copy = {...model, ID: 0}
      delete copy.id
      delete copy.CreatedAt
      delete copy.UpdatedAt
      return copy
    })
  }
  return result
}
