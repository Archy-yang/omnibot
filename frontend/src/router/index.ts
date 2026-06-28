import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'Home',
    component: () => import('@/views/ChatPage.vue'),
    meta: {
      title: 'OmniBot - 智能助手',
    },
  },
  {
    path: '/memory',
    name: 'Memory',
    component: () => import('@/views/MemoryPage.vue'),
    meta: {
      title: 'OmniBot - 长期记忆',
    },
  },
];

const router = createRouter({
  history: createWebHistory(),
  routes,
});

// Set page title
router.beforeEach((to, _from, next) => {
  document.title = (to.meta?.title as string) || 'OmniBot';
  next();
});

export default router;
