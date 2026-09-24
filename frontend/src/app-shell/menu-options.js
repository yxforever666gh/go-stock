import {h} from 'vue'
import {RouterLink} from 'vue-router'
import {NIcon} from 'naive-ui'
import AnalyticsOutline from '@vicons/ionicons5/es/AnalyticsOutline.js'
import InformationCircleOutline from '@vicons/ionicons5/es/InformationCircleOutline.js'
import SettingsOutline from '@vicons/ionicons5/es/SettingsOutline.js'

import {navigation} from './navigation.js'
const icons = {prediction: AnalyticsOutline, settings: SettingsOutline, about: InformationCircleOutline}

export function createMenuOptions() {
  return navigation.map(({key, label}) => ({
    key,
    label: () => h(RouterLink, {to: {name: key}}, {default: () => label}),
    icon: () => h(NIcon, null, {default: () => h(icons[key])}),
  }))
}
