import { createRouter, createWebHashHistory } from 'vue-router'

const stockView = () => import('../views/StockView.vue')
const settingsView = () => import('../views/SettingsView.vue')
const aboutView = () => import('../views/AboutView.vue')
const fundView = () => import('../views/FundView.vue')
const marketView = () => import('../views/MarketView.vue')
const agentChat = () => import('../views/AgentView.vue')
const research = () => import('../views/ResearchView.vue')

const routes = [
  { path: '/', component: stockView, name: 'stock' },
  { path: '/fund', component: fundView, name: 'fund' },
  { path: '/settings', component: settingsView, name: 'settings' },
  { path: '/about', component: aboutView, name: 'about' },
  { path: '/market', component: marketView, name: 'market' },
  { path: '/agent', component: agentChat, name: 'agent' },
  { path: '/research', component: research, name: 'research' },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

export default router
