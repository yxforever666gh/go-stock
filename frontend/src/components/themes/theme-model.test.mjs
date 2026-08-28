import assert from 'node:assert/strict'
import test from 'node:test'
import {
  THEME_STAGES,
  credibilityPercent,
  normalizeThemeDetail,
  stanceLabel,
  themeCatalysts,
  themeListItems,
  themeSnapshots,
} from './theme-model.js'

test('theme list preserves lifecycle change and representative securities without inventing strength', () => {
  const [theme] = themeListItems({items: [{
    themeId: 't1', name: '机器人', lifecycleStage: '加速', previousLifecycleStage: '发酵', stageChanged: true,
    heatScore: 88, rank: 1, representativeSecurities: [{assetType: 'stock', market: 'sz', code: '000001', name: '代表股'}],
  }]})
  assert.deepEqual(THEME_STAGES, ['观察', '发酵', '加速', '分歧', '退潮'])
  assert.equal(theme.previousLifecycleStage, '发酵')
  assert.equal(theme.stageChanged, true)
  assert.equal(theme.representativeSecurities[0].market, 'SZ')
  assert.equal(theme.heatScore, 88)
})

test('daily snapshots stay chronological and detail keeps full constituents', () => {
  const snapshots = themeSnapshots({items: [
    {snapshotId: '2', tradeDate: '2026-01-02', lifecycleStage: '发酵'},
    {snapshotId: '1', tradeDate: '2026-01-01', lifecycleStage: '观察'},
  ]})
  assert.deepEqual(snapshots.map(item => item.lifecycleStage), ['观察', '发酵'])
  const detail = normalizeThemeDetail({theme: {themeId: 't1', name: '题材'}, constituents: [{constituentId: 'c1', market: 'sh', code: '600000', name: '成分'}]})
  assert.equal(detail.constituents[0].market, 'SH')
})

test('normalizers also accept repository-native nested identity shapes', () => {
  const [item] = themeListItems({items: [{
    id: 'native', canonicalName: '原生题材', aliases: [{alias: '别名'}],
    snapshot: {id: 'snapshot', themeId: 'native', lifecycleStage: '观察', heatScore: 60},
  }]})
  assert.equal(item.themeId, 'native')
  assert.equal(item.snapshotId, 'snapshot')
  assert.deepEqual(item.aliases, ['别名'])
  const detail = normalizeThemeDetail({theme: {
    id: 'native', canonicalName: '原生题材', aliases: [{alias: '别名'}],
    snapshot: {id: 'snapshot', lifecycleStage: '观察', constituents: [{id: 'c', market: 'sz', code: '000001'}]},
  }})
  assert.equal(detail.snapshot.snapshotId, 'snapshot')
  assert.equal(detail.constituents[0].market, 'SZ')
})

test('supports and contradicts claims remain side by side with typo-compatible credibility', () => {
  const [event] = themeCatalysts({items: [{
    catalystEventId: 'e1', title: '政策', hasConflict: true, eventAt: '2026-01-01T09:00:00Z', firstAvailableAt: '2026-01-01T09:01:00Z',
    sources: [
      {sourceClaimId: 's1', sourceName: '甲', stance: 'supports', sourceCredibilityScore: 0.9, availableAt: 'a'},
      {sourceClaimId: 's2', sourceName: '乙', stance: 'contradicts', souceCredibilityScore: 70, availableAt: 'b'},
    ],
  }]})
  assert.equal(event.sources.length, 2)
  assert.equal(stanceLabel(event.sources[0].stance).label, '支持')
  assert.equal(stanceLabel(event.sources[1].stance).label, '反驳')
  assert.equal(credibilityPercent(event.sources[0].sourceCredibilityScore), '90%')
  assert.equal(credibilityPercent(event.sources[1].sourceCredibilityScore), '70%')
})
