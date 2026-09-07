import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'
import {compileScript, parse} from '@vue/compiler-sfc'
import {createSSRApp} from 'vue'
import {renderToString} from '@vue/server-renderer'

const source = await readFile(new URL('./AppMarkdownPreview.vue', import.meta.url), 'utf8')
const {descriptor} = parse(source)
const compiled = compileScript(descriptor, {id: 'markdown-safety-test', inlineTemplate: true})
const script = compiled.content.replace(/from (['"])([^'"]+)\1/g, (_, quote, specifier) => {
  const resolved = specifier.startsWith('.') ? new URL(specifier, import.meta.url).href : import.meta.resolve(specifier)
  return `from ${JSON.stringify(resolved)}`
})
const {default: Preview} = await import(`data:text/javascript;base64,${Buffer.from(script).toString('base64')}`)
const render = (modelValue, attrs = {}) => renderToString(createSSRApp(Preview, {modelValue, noHighlight: true, noKatex: true, ...attrs}))

test('report HTML cannot add event handlers, executable URLs or embedded documents', async () => {
  const html = await render('<img src="x" onerror="alert(1)"><a href="jav&#x61;script:alert(2)">bad</a><iframe srcdoc="bad"></iframe><svg onload="alert(3)"></svg>')
  assert.doesNotMatch(html, /onerror=|onload=|javascript:|<iframe|<svg|srcdoc=/i)
  assert.match(html, /bad/)
})

test('final rendered code-fence language labels are sanitized too', async () => {
  const html = await render('```js <img src=x onerror="alert(1)">\nconst value = 1\n```')
  assert.doesNotMatch(html, /onerror=/i)
  assert.match(html, /const value = 1/)
})

test('callers cannot bypass the safety policy through forwarded attrs', async () => {
  const html = await render('<img src=x onerror="alert(1)">', {sanitize: value => value, noMermaid: false})
  assert.doesNotMatch(html, /onerror=/i)
  const diagram = await render('```mermaid\ngraph TD; A-->B\n```', {noMermaid: false})
  assert.doesNotMatch(diagram, /class="[^"]*md-editor-mermaid/)
  assert.match(diagram, /graph TD/)
})

test('ordinary Chinese reports, tables, links and code remain readable', async () => {
  const html = await render('# 分析结论\n\n| 股票 | 收益 |\n|---|---|\n| 中国平安 | -3.05% |\n\n[来源](https://example.com/report)\n\n```js\nconst price = 56.58\n```')
  assert.match(html, /<h1[^>]*>分析结论/)
  assert.match(html, /<table/)
  assert.match(html, /中国平安/)
  assert.match(html, /href="https:\/\/example.com\/report"/)
  assert.match(html, /const price = 56.58/)
})
