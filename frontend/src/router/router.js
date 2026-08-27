import { createRouter, createWebHashHistory } from 'vue-router'

const stockView = () => import('../views/StockView.vue')
const aboutView = () => import('../views/AboutView.vue')
const fundView = () => import('../views/FundView.vue')
const marketView = () => import('../views/MarketView.vue')
const research = () => import('../views/ResearchView.vue')
const research2 = () => import('../views/Research2View.vue')

const routes = [
  { path: '/', component: stockView, name: 'stock' },
  { path: '/fund', component: fundView, name: 'fund' },
  { path: '/settings', redirect: { name: 'research', query: { name: '设置' } } },
  { path: '/about', component: aboutView, name: 'about' },
  { path: '/market', component: marketView, name: 'market' },
  { path: '/research', component: research, name: 'research' },
  { path: '/research2', component: research2, name: 'research2' },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

export default router
