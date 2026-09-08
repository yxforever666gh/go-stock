import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'
import {compileScript, compileTemplate, parse} from '@vue/compiler-sfc'
import {createRenderer} from 'vue'

const moduleURL = source => `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
const uiStub = moduleURL("export const NButton='button', NTag='tag', NText='text'; export const useMessage=()=>({error(){},warning(){},success(){}})")
const childStub = moduleURL('export default {render(){return null}}')
const dragStub = moduleURL(`import {ref} from ${JSON.stringify(import.meta.resolve('vue'))}; export const useDraggableDataTableColumns=columns=>({tableRef:ref(null),columnsRef:ref(columns)})`)
const renderer = createRenderer({
  createComment: text => ({text}), insert() {}, remove() {}, parentNode: () => null,
  nextSibling: () => null, createElement: tag => ({tag}), createText: text => ({text}),
  setText() {}, setElementText() {}, patchProp() {},
})
const flush = async () => { await new Promise(setImmediate); await new Promise(setImmediate) }

async function pageComponent(filename) {
  const url = new URL(filename, import.meta.url)
  const {descriptor} = parse(await readFile(url, 'utf8'), {filename})
  const compiled = compileScript(descriptor, {id: filename})
  const template = compileTemplate({source: descriptor.template.content, filename, id: filename, compilerOptions: {bindingMetadata: compiled.bindings}})
  assert.deepEqual(template.errors, [], `${filename} template compiles`)
  const script = compiled.content.replace(/import\s*\{([^}]+)\}\s*from\s*(['"])([^'"]+)\2/g, (whole, names, quote, specifier) => {
    if (!specifier.includes('/services/') || specifier.endsWith('.js')) return whole
    const exports = names.split(',').map(name => name.trim()).filter(Boolean).map(name => `export const ${name}=(...args)=>globalThis.__researchPageFixtures[${JSON.stringify(name)}](...args)`).join(';')
    return `import {${names}} from ${JSON.stringify(moduleURL(exports))}`
  }).replace(/from (['"])([^'"]+)\1/g, (_, quote, specifier) => {
    let resolved = specifier
    if (specifier === 'naive-ui') resolved = uiStub
    else if (specifier.endsWith('.vue')) resolved = childStub
    else if (specifier.includes('useDraggableDataTableColumns')) resolved = dragStub
    else if (specifier.startsWith('.')) resolved = new URL(/\.(?:js|mjs)$/.test(specifier) ? specifier : `${specifier}.js`, url).href
    else if (!specifier.startsWith('data:')) resolved = import.meta.resolve(specifier)
    return `from ${JSON.stringify(resolved)}`
  })
  const {default: component} = await import(moduleURL(script))
  return {...component, render: () => null}
}

test('research2 shows daily buy slots without reusing original batch ranks, and explains historical scores', async () => {
  const rows = [
    {recommendationId: 'first', selectionRole: 'primary', selectionRank: 1, displaySelectionRole: 'primary', displaySelectionRank: 1},
    {recommendationId: 'second', selectionRole: 'primary', selectionRank: 2, displaySelectionRole: 'primary', displaySelectionRank: 2},
    {recommendationId: 'promoted', selectionRole: 'standby', selectionRank: 4, displaySelectionRole: 'primary', displaySelectionRank: 3},
    {recommendationId: 'pending', selectionRole: 'primary', selectionRank: 1, displaySelectionRole: 'pending', displaySelectionRank: 0},
    {recommendationId: 'standby', selectionRole: 'standby', selectionRank: 5, displaySelectionRole: 'standby', displaySelectionRank: 1},
    {recommendationId: 'failed', selectionRole: 'primary', selectionRank: 1, displaySelectionRole: '', displaySelectionRank: 0},
  ]
  globalThis.__researchPageFixtures = {GetResearch2Account: async () => ({}), ListResearch2Recommendations: async () => rows}
  const app = renderer.createApp(await pageComponent('research2Recommendations.vue'))
  const vm = app.mount({})
  try {
    await flush()
    const state = vm.$.setupState
    const role = state.columnsRef.find(column => column.key === 'selectionRole')
    assert.deepEqual(state.rows.map(role.render), ['主选 #1', '主选 #2', '主选 #3', '待补位', '备选 #1', '--'])
    assert.equal(state.hasScoreExplanation({reportMarkdown: '# 历史报告'}), false)
    assert.equal(state.hasScoreExplanation({reportMarkdown: '# 报告\n\n## 分项评分依据\n市场18分'}), true)
    const source = await readFile(new URL('research2Recommendations.vue', import.meta.url), 'utf8')
    assert.match(source, /原始批次主备/)
    assert.match(source, /历史未记录逐项评分说明/)
    for (const field of ['marketScore', 'sectorScore', 'stockScore', 'catalystScore', 'riskDeduction']) assert.ok(source.includes(`detail.recommendation.${field}`))
  } finally {
    app.unmount()
    delete globalThis.__researchPageFixtures
  }
})

for (const filename of ['researchRecommendations.vue', 'research2Recommendations.vue', 'researchYield.vue', 'research2Yield.vue', 'researchReport.vue', 'research2Report.vue']) {
  test(`${filename}: all 201 historical rows remain reachable and details reject late responses`, async () => {
    const rows = Array.from({length: 201}, (_, index) => ({recommendationId: `r${index}`, runId: `r${index}`, activatedAt: '2026-09-07', status: 'closed'}))
    const details = new Map()
    globalThis.__researchPageFixtures = new Proxy({}, {get: (_, name) => {
      if (String(name).startsWith('List') && !String(name).includes('CashFlows')) return async (limit, offset) => rows.slice(offset, offset + limit)
      if (String(name).includes('Recommendation') || name === 'GetAIAnalysisReport' || name === 'GetResearch2Run') return id => new Promise(resolve => details.set(id, resolve))
      return async () => ({})
    }})
    const app = renderer.createApp(await pageComponent(filename))
    const vm = app.mount({})
    try {
      const state = vm.$.setupState
      await flush()
      while (state.history.hasMore.value) await state.history.loadMore()
      assert.equal(state.rows.length, 201)
      const show = state.showDetail || state.show
      const a = show({recommendationId: 'a', runId: 'a'}), b = show({recommendationId: 'b', runId: 'b'})
      details.get('b')({id: 'b'}); await b
      details.get('a')({id: 'a'}); await a
      assert.equal(state.detail.id, 'b')
      await state.history.refresh()
      assert.equal(state.rows.length, filename.includes('Report') ? 100 : 200)
    } finally {
      app.unmount()
      delete globalThis.__researchPageFixtures
    }
  })
}

test('research2 refreshes a pending detail when its list reaches a terminal state first', async () => {
  let finishOldDetail, detailReads = 0, listStatus = 'running'
  globalThis.__researchPageFixtures = {
    ListResearch2Runs: async () => [{runId: 'r', status: listStatus}],
    GetResearch2Run: async () => {
      detailReads++
      if (detailReads === 1) return await new Promise(resolve => { finishOldDetail = resolve })
      return {runId: 'r', status: 'success'}
    },
  }
  const app = renderer.createApp(await pageComponent('research2Report.vue'))
  const vm = app.mount({})
  try {
    const state = vm.$.setupState
    await flush()
    const original = state.show({runId: 'r'})
    listStatus = 'success'
    await state.polling.run()
    assert.equal(state.rows[0].status, 'success')
    finishOldDetail({runId: 'r', status: 'running'})
    await original
    assert.equal(state.detail.status, 'success')
    assert.equal(state.detailLoading, false)
    assert.equal(detailReads, 2)
  } finally {
    app.unmount()
    delete globalThis.__researchPageFixtures
  }
})
