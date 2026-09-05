import assert from 'node:assert/strict'
import test from 'node:test'
import {
  confidencePresentation,
  formatDocumentShare,
  hotWordTrend,
  normalizeHotWordsPayload,
  sentimentScale,
} from './market-hot-words-model.js'

test('hot words payload normalizes ranking fields and limits representative news', () => {
  const payload = normalizeHotWordsPayload({
    baseline: {available: true, documentCount: 800},
    currentDocumentCount: 120,
    sentiment: {score: 18.25, description: '看涨'},
    items: [{
      rank: 1,
      word: '人工智能',
      documentCount: 12,
      occurrenceCount: 20,
      documentShare: 0.1,
      baselineDocumentCount: 2,
      burstRatio: 4.8,
      sourceCount: 2,
      sources: ['财联社', '新浪财经', '财联社'],
      confidence: 'HIGH',
      representativeNews: [1, 2, 3, 4].map(index => ({
        publishedAt: `2026-08-29T0${index}:00:00+08:00`,
        source: '财联社',
        excerpt: `新闻 ${index}`,
        url: `https://example.com/${index}`,
      })),
    }],
  })

  assert.equal(payload.baseline.available, true)
  assert.equal(payload.currentDocumentCount, 120)
  assert.equal(payload.sentiment.score, 18.25)
  assert.equal(payload.sentiment.label, '看涨')
  assert.equal(payload.items[0].confidence, 'high')
  assert.deepEqual(payload.items[0].sources, ['财联社', '新浪财经'])
  assert.equal(payload.items[0].representativeNews.length, 3)
})

test('sentiment scale clamps values and maps them onto the horizontal band', () => {
  assert.deepEqual([-100, -50, 0, 50, 100].map(value => sentimentScale(value).position), [0, 25, 50, 75, 100])
  assert.equal(sentimentScale(-200).score, -100)
  assert.equal(sentimentScale(200).score, 100)
  assert.deepEqual([-75, -25, 25, 75].map(value => sentimentScale(value).tone), ['ice', 'cautious', 'optimistic', 'hot'])
})

test('trend presentation distinguishes fallback, new words, growth and burst ratio', () => {
  assert.deepEqual(hotWordTrend({baselineDocumentCount: 3}, false), {label: '暂无可靠基线', type: 'warning'})
  assert.deepEqual(hotWordTrend({baselineDocumentCount: 0}, true), {label: '新出现', type: 'error'})
  assert.deepEqual(hotWordTrend({baselineDocumentCount: 2, growthPct: 125}, true), {label: '+125.0%', type: 'error'})
  assert.deepEqual(hotWordTrend({baselineDocumentCount: 2, growthPct: -25}, true), {label: '-25.0%', type: 'success'})
  assert.deepEqual(hotWordTrend({baselineDocumentCount: 2, burstRatio: 1.75}, true), {label: '1.75×', type: 'error'})
})

test('hot word metric labels keep contract units and confidence levels', () => {
  assert.equal(formatDocumentShare(0.125), '12.5%')
  assert.equal(formatDocumentShare(null), '--')
  assert.deepEqual(confidencePresentation('high'), {label: '高', type: 'success'})
  assert.deepEqual(confidencePresentation('medium'), {label: '中', type: 'info'})
  assert.deepEqual(confidencePresentation('unexpected'), {label: '低', type: 'warning'})
})
