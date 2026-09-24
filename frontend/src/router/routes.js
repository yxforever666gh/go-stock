export const routes = [
  {path: '/', redirect: {name: 'prediction'}},
  {path: '/prediction', name: 'prediction', component: () => import('../views/PredictionView.vue')},
  {path: '/settings', name: 'settings', component: () => import('../views/SettingsView.vue')},
  {path: '/about', name: 'about', component: () => import('../views/AboutView.vue')},
  {path: '/:pathMatch(.*)*', redirect: {name: 'prediction'}},
]
