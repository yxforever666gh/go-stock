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

test('research1 separates collection, input coverage and no-match news', async () => {
  globalThis.__researchPageFixtures = {ListAIAnalysisReports: async () => [], GetAICapitalDeploymentStatus: async () => ({})}
  const app = renderer.createApp(await pageComponent('researchReport.vue'))
  const vm = app.mount({})
  try {
    await flush()
    const state = vm.$.setupState
    const sources = [
      {sourceId: 'S1', collectionStatus: 'ok', inputStatus: 'summarized'},
      {sourceId: 'S2', collectionStatus: 'no_match', inputStatus: 'included'},
      {sourceId: 'S3', collectionStatus: 'ok', error: 'stale', inputStatus: 'unavailable', inputReason: 'stale'},
      {sourceId: 'S4', collectionStatus: 'ok', inputStatus: 'omitted', inputReason: '超出每股输入预算'},
      {sourceId: 'S5'},
      {sourceId: 'S6', collectionStatus: 'failed', error: 'timeout'},
      {sourceId: 'S7', collectionStatus: 'ok', inputStatus: 'included', filteredMinuteCount: 1},
    ]
    const rows = state.sourceRows(JSON.stringify(sources))
    assert.deepEqual(rows.map(row => row.inputStatus), ['已摘要', '已提供', '不可用', '省略', '历史未记录', '历史未记录', '已提供'])
    assert.equal(rows[1].status, '无匹配新闻')
    assert.match(rows[1].inputReason, /不代表公司没有风险/)
    assert.equal(rows[2].status, '成功')
    assert.equal(rows[5].status, '失败')
    assert.match(rows[6].inputReason, /已过滤 1 条/)
    assert.equal(state.sourceSummary(JSON.stringify(sources)), '7 个来源，2 个失败，1 个无匹配新闻')
    assert.equal(state.summarySourceStatus({sourceCount: 7, failedSourceCount: 2, noMatchNewsCount: 1}), '7 个来源，2 个失败，1 个无匹配新闻')
    assert.equal(state.summarySourceStatus({sourceCount: 2, failedSourceCount: 1}), '2 个来源，1 个失败，0 个无匹配新闻')
  } finally {
    app.unmount()
    delete globalThis.__researchPageFixtures
  }
})

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

test('research2 account switch isolates list requests and ignores an unmounted account response', async () => {
  const pending = new Map()
  globalThis.__researchPageFixtures = {
    GetResearch2Account: async () => ({}),
    ListResearch2Recommendations: (limit, offset, slot) => new Promise(resolve => pending.set(slot, resolve)),
  }
  const component = await pageComponent('research2Recommendations.vue')
  const first = renderer.createApp(component, {slot: '09:30'})
  first.mount({})
  await flush()
  first.unmount()
  const second = renderer.createApp(component, {slot: '09:35'})
  const vm = second.mount({})
  try {
    await flush()
    assert.deepEqual([...pending.keys()], ['09:30', '09:35'])
    pending.get('09:35')([{recommendationId: 'new-account'}])
    await flush()
    pending.get('09:30')([{recommendationId: 'old-account'}])
    await flush()
    assert.deepEqual(vm.$.setupState.rows.map(row => row.recommendationId), ['new-account'])
  } finally {
    second.unmount()
    delete globalThis.__researchPageFixtures
  }
})

test('research2 uses server ranks and execution states without selection roles or score gates', async () => {
  const rows = [
    {recommendationId: 'first', selectionRank: 1, displaySelectionRank: 1, finalScore: 51, status: 'buy_pending'},
    {recommendationId: 'second', selectionRank: 2, displaySelectionRank: 2, finalScore: 50, status: 'buy_pending'},
    {recommendationId: 'promoted', selectionRole: 'observation', selectionRank: 4, displaySelectionRank: 3, finalScore: 30, status: 'active', buyAt: '2026-09-15T09:52:00+08:00', buyPrice: 10, quantity: 100},
    {recommendationId: 'pending', selectionRole: 'primary', selectionRank: 1, displaySelectionRole: 'primary', displaySelectionRank: 3, finalScore: 57, status: 'buy_pending'},
    {recommendationId: 'standby', selectionRole: 'observation', status: 'analysis_only', selectionRank: 5, displaySelectionRole: 'candidate', displaySelectionRank: 3},
    {recommendationId: 'failed', selectionRole: 'primary', selectionRank: 1, displaySelectionRole: '', displaySelectionRank: 0},
  ]
  globalThis.__researchPageFixtures = {GetResearch2Account: async () => ({}), ListResearch2Recommendations: async () => rows}
  const app = renderer.createApp(await pageComponent('research2Recommendations.vue'))
  const vm = app.mount({})
  try {
    await flush()
    const state = vm.$.setupState
    const rank = state.columnsRef.find(column => column.key === 'displaySelectionRank')
    assert.equal(rank.title, '排名')
    assert.equal(state.columnsRef.some(column => column.key === 'selectionRole'), false)
    assert.deepEqual(state.rows.map(rank.render), ['1', '2', '3', '3', '3', '--'])
    assert.equal(state.statusLabel(rows[0]), '待买入')
    assert.equal(state.statusLabel(rows[1]), '待买入')
    assert.equal(state.statusLabel(rows[2]), '持仓中')
    assert.equal(state.statusLabel(rows[3]), '待买入')
    assert.equal(state.statusLabel(rows[4]), '仅分析')
    assert.equal(state.columnsRef.find(column => column.key === 'quantity').render(rows[4]), '--')
    assert.equal(state.columnsRef.find(column => column.key === 'netYieldRate').render(rows[4]), '--')
    assert.equal(state.hasScoreExplanation({reportMarkdown: '# 历史报告'}), false)
    assert.equal(state.hasScoreExplanation({reportMarkdown: '# 报告\n\n## 分项评分依据\n市场18分'}), true)
    const source = await readFile(new URL('research2Recommendations.vue', import.meta.url), 'utf8')
    assert.match(source, /报告排名/)
    assert.doesNotMatch(source, /主选|候选|selectionRole|displaySelectionRole/)
    assert.match(source, /本分区以本轮报告落盘时的可用现金/)
    assert.match(source, /达到或超过五分之一时买一手/)
    assert.match(source, /其余成交严格低于五分之一/)
    assert.match(source, /任何成交均不得超过当前可用现金/)
    assert.match(source, /历史未记录逐项评分说明/)
    for (const field of ['marketScore', 'sectorScore', 'stockScore', 'catalystScore', 'riskDeduction']) assert.ok(source.includes(`detail.recommendation.${field}`))
  } finally {
    app.unmount()
    delete globalThis.__researchPageFixtures
  }
})

test('research2 keeps the five-minute account selector collapsed until clicked', async () => {
  const source = await readFile(new URL('research2Index.vue', import.meta.url), 'utf8')
  assert.match(source, /<n-dropdown trigger="click"/)
  assert.match(source, /slotOptions/)
  assert.match(source, /:render-label="renderSlotLabel"/)
  assert.match(source, /:node-props="slotNodeProps"/)
  assert.match(source, /research2-slot-option-current/)
  assert.match(source, /当前选择/)
  assert.match(source, /买入：等待报告/)
  assert.doesNotMatch(source, /research2SlotReportLabel|reportTagType|持仓：/)
  assert.doesNotMatch(source, /已完成|尚未完成/)
  assert.match(source, /选择五分钟区间/)
  assert.doesNotMatch(source, /<n-tab v-for="slot in RESEARCH2_SLOTS"/)
})

test('research2 report explains the one-report daily limit and does not offer another analysis', async () => {
  globalThis.__researchPageFixtures = {ListResearch2Runs: async () => [], GetResearch2Run: async () => ({})}
  const app = renderer.createApp(await pageComponent('research2Report.vue'))
  const vm = app.mount({})
  try {
    await flush()
    const state = vm.$.setupState
    assert.equal(state.columns.some(column => column.key === 'selectionCounts'), false)
    assert.equal(state.columns.find(column => column.key === 'attemptNo').title, '尝试')
    for (const status of ['failed', 'success', 'no_recommendation']) {
      const action = state.columns.find(column => column.key === 'action').render({status, runId: status})
      assert.equal(action.children, '查看')
    }
    const source = await readFile(new URL('research2Report.vue', import.meta.url), 'utf8')
    assert.match(source, /09:30至11:25每五分钟独立启动/)
    assert.match(source, /成功按落盘时间归区间/)
    assert.match(source, /卖出由各账户的定时任务独立执行/)
    assert.doesNotMatch(source, /主选|候选|补位|主备|主\/备|09:55/)
  } finally {
    app.unmount()
    delete globalThis.__researchPageFixtures
  }
})

test('research2 yield keeps unbought rows out of displayed returns regardless of score and legacy role', async () => {
  const rows = [
    {recommendationId: 'pending', status: 'buy_pending', finalScore: 10, netPnl: 0, netYieldRate: 0},
    {recommendationId: 'skipped', status: 'missed_cash', finalScore: 90, netPnl: 0, netYieldRate: 0},
    {recommendationId: 'analysis', status: 'analysis_only', selectionRole: 'observation', netPnl: 0, netYieldRate: 0},
    {recommendationId: 'bought', status: 'active', selectionRole: 'observation', finalScore: 0, buyAt: '2026-09-15T09:52:00+08:00', buyPrice: 10, quantity: 100, netPnl: 8, netYieldRate: 0.008},
  ]
  globalThis.__researchPageFixtures = {GetResearch2Performance: async () => ({}), ListResearch2Recommendations: async () => rows}
  const app = renderer.createApp(await pageComponent('research2Yield.vue'))
  const vm = app.mount({})
  try {
    await flush()
    const state = vm.$.setupState
    for (const key of ['quantity', 'netPnl', 'netYieldRate', 'hitFiveBeforeSell', 'hitLimitUpFullDay', 'hitMinusThree']) {
      const column = state.columns.find(column => column.key === key)
      for (const row of rows.slice(0, 3)) assert.equal(column.render(row), '--')
    }
    assert.equal(state.columns.find(column => column.key === 'quantity').render(rows[3]), '100')
    assert.notEqual(state.columns.find(column => column.key === 'netPnl').render(rows[3]), '--')
    assert.equal(state.hasBuy(rows[3]), true)
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

for (const filename of ['researchRecommendations.vue', 'research2Recommendations.vue']) {
  test(`${filename}: Shanghai date separators span loaded pages and tolerate missing dates`, async () => {
    const rows = Array.from({length: 200}, (_, index) => ({recommendationId: `r${index}`, signalAt: '2026-09-10T00:05:00+08:00'}))
    rows.push(
      {recommendationId: 'older', signalAt: '2026-09-09T15:59:00Z'},
      {recommendationId: 'same', signalAt: '2026-09-09 23:58:00'},
      {recommendationId: 'missing'},
      {recommendationId: 'after-missing', signalAt: '2026-09-08'},
      {recommendationId: 'invalid', signalAt: 'invalid'},
    )
    globalThis.__researchPageFixtures = new Proxy({}, {get: (_, name) => String(name).startsWith('List') ? async (limit, offset) => rows.slice(offset, offset + limit) : async () => ({})})
    const app = renderer.createApp(await pageComponent(filename))
    const vm = app.mount({})
    try {
      await flush()
      const state = vm.$.setupState
      assert.equal(state.rows.length, 200)
      assert.equal(state.recommendationRowClass(state.rows[0], 0), '')
      assert.equal(state.recommendationRowClass(state.rows[199], 199), '')
      assert.equal(state.signalDate('2026-09-09T16:05:00Z'), state.signalDate(state.rows[0].signalAt))
      await state.history.loadMore()
      assert.equal(state.recommendationRowClass(state.rows[200], 200), 'recommendation-date-start')
      assert.equal(state.recommendationRowClass(state.rows[201], 201), '')
      for (const index of [202, 203, 204]) assert.equal(state.recommendationRowClass(state.rows[index], index), '')
      const source = await readFile(new URL(filename, import.meta.url), 'utf8')
      assert.equal((source.match(/:row-class-name="recommendationRowClass"/g) || []).length, 1)
      assert.match(source, /:deep\(\.recommendation-date-start > td\)\s*\{\s*border-top: 2px solid #000;/)
      rows.length = 0
      await state.history.refresh()
      assert.deepEqual(state.rows, [])
    } finally {
      app.unmount()
      delete globalThis.__researchPageFixtures
    }
  })
}
