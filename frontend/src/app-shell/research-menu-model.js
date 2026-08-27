export const RESEARCH_CENTER1_LABEL = '研究中心1'
export const RESEARCH_CENTER2_LABEL = '研究中心2'
export const RESEARCH_TABS = ['股票推荐记录', 'AI分析报告', '股票收益率', '设置']

export function researchCenterMenuModel() {
  return [
    {key: 'research', label: RESEARCH_CENTER1_LABEL, routeName: 'research', settingsScope: 'research1', tabs: [...RESEARCH_TABS]},
    {key: 'research2', label: RESEARCH_CENTER2_LABEL, routeName: 'research2', settingsScope: 'research2', tabs: [...RESEARCH_TABS]},
  ]
}
