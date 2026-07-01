import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';
import { useAuthStore } from '@/stores/user';

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'Home',
    component: () => import('@/views/ChatPage.vue'),
    meta: {
      title: 'OmniBot - 智能助手',
      requiresAuth: true,
    },
  },
  {
    path: '/login',
    name: 'Login',
    component: () => import('@/views/LoginPage.vue'),
    meta: {
      title: '登录 - OmniBot',
      requiresGuest: true,
    },
  },
  {
    path: '/register',
    name: 'Register',
    component: () => import('@/views/RegisterPage.vue'),
    meta: {
      title: '注册 - OmniBot',
      requiresGuest: true,
    },
  },
];

const router = createRouter({
  history: createWebHistory(),
  routes,
});

// v2.1: 路由守卫
// - requiresAuth 页面无 token → 跳 /login
// - requiresGuest 页面已登录 → 跳 /(避免登录/注册页给已登录用户看)
// - 顺带设置页面标题
router.beforeEach((to, _from, next) => {
  document.title = (to.meta?.title as string) || 'OmniBot';

  const auth = useAuthStore();

  if (to.meta?.requiresAuth && !auth.isAuthenticated) {
    next({ name: 'Login' });
    return;
  }
  if (to.meta?.requiresGuest && auth.isAuthenticated) {
    next({ name: 'Home' });
    return;
  }
  next();
});

export default router;
