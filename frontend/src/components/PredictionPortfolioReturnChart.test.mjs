import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'
import {compileScript, parse} from '@vue/compiler-sfc'
import {createRenderer, h, nextTick, ref} from 'vue'

const moduleURL = value => `data:text/javascript;base64,${Buffer.from(value).toString('base64')}`

test('portfolio chart initializes after asynchronous data and survives empty-range transitions', async () => {
  const charts = []
  const priorObserver = globalThis.ResizeObserver
  globalThis.__portfolioChartFixture = charts
  globalThis.ResizeObserver = class { observe() {} disconnect() {} }
  const echarts = moduleURL(`export function init(node) {
    const chart = {node, disposed:false, getDom(){return node}, isDisposed(){return this.disposed},
      dispose(){this.disposed=true}, resize(){}, setOption(value){this.option=value}};
    globalThis.__portfolioChartFixture.push(chart); return chart;
  }`)
  const file = new URL('./PredictionPortfolioReturnChart.vue', import.meta.url)
  const {descriptor} = parse(await readFile(file, 'utf8'))
  const compiled = compileScript(descriptor, {id: 'portfolio-fixture', inlineTemplate: true})
  const code = compiled.content.replace(/from (['"])([^'"]+)\1/g, (_, quote, name) => {
    const target = name === 'echarts' ? echarts : name.startsWith('.') ? new URL(`${name}.js`, file).href : import.meta.resolve(name)
    return `from ${JSON.stringify(target)}`
  })
  const component = (await import(moduleURL(code))).default
  const renderer = createRenderer({
    createComment: text => ({text}), insert() {}, remove() {}, parentNode: () => null,
    nextSibling: () => null, createElement: tag => ({tag}), createText: text => ({text}),
    setText() {}, setElementText() {}, patchProp() {},
  })
  const points = ref([])
  const app = renderer.createApp({render: () => h(component, {points: points.value})})
  app.component('n-empty', {render: () => h('div')})
  app.mount({})
  const settle = async () => { await nextTick(); await nextTick() }
  try {
    await settle()
    assert.equal(charts.length, 0)
    points.value = [{tradingDate: '2026-09-24', returnRate: 0.025, effectiveAccountCount: 24}]
    await settle()
    assert.equal(charts.length, 1)
    assert.deepEqual(charts[0].option.series[0].data, [0.025])
    points.value = []
    await settle()
    assert.equal(charts[0].disposed, true)
    points.value = [{tradingDate: '2026-09-25', returnRate: -0.01, effectiveAccountCount: 24}]
    await settle()
    assert.equal(charts.length, 2)
    assert.notEqual(charts[0].node, charts[1].node)
    assert.deepEqual(charts[1].option.series[0].data, [-0.01])
  } finally {
    app.unmount()
    globalThis.ResizeObserver = priorObserver
    delete globalThis.__portfolioChartFixture
  }
  assert.equal(charts.at(-1).disposed, true)
})
