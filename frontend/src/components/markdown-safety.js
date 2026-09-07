import xss from 'xss'

const whiteList = xss.getDefaultWhiteList()
for (const tag of ['audio', 'video', 'source']) delete whiteList[tag]
for (const attributes of Object.values(whiteList)) attributes.push('class')
for (const tag of ['h1', 'h2', 'h3', 'h4', 'h5', 'h6']) whiteList[tag].push('id')
whiteList.code.push('language')
whiteList.span.push('data-tips', 'rn-wrapper', 'aria-hidden')
whiteList.a.push('rel')

const sanitizer = new xss.FilterXSS({
  whiteList,
  stripIgnoreTag: true,
  stripIgnoreTagBody: ['script', 'style', 'iframe', 'object', 'embed', 'svg', 'math'],
})

// Apply after Markdown and its code plugins have produced their final HTML.
export function sanitizeReportHTML(html) {
  return sanitizer.process(String(html ?? ''))
}

export function safePreviewAttrs(attrs) {
  return Object.fromEntries(Object.entries(attrs).filter(([key]) => !['sanitize', 'sanitizemermaid', 'nomermaid'].includes(key.replaceAll('-', '').toLowerCase())))
}
