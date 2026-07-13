<script setup lang="ts">
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import { authService } from '@/services/auth';
import { useAuthStore } from '@/stores/user';
import { APP_NAME, APP_VERSION } from '@/constants/about';

const router = useRouter();
const auth = useAuthStore();

const email = ref('');
const password = ref('');
const errorMsg = ref('');
const isSubmitting = ref(false);
const showPassword = ref(false);

const submit = async () => {
  if (!email.value || !password.value) {
    errorMsg.value = '请输入邮箱和密码';
    return;
  }
  errorMsg.value = '';
  isSubmitting.value = true;
  try {
    const token = await authService.login(email.value, password.value);
    auth.setToken(token);
    router.push('/');
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : '登录失败';
  } finally {
    isSubmitting.value = false;
  }
};

const togglePassword = () => {
  showPassword.value = !showPassword.value;
};
</script>

<template>
  <div class="auth-page">
    <!-- 品牌标识 -->
    <div class="brand-icon">
      <span>O</span>
    </div>

    <!-- 登录卡片 -->
    <div class="auth-card">
      <!-- 标题区 -->
      <div class="card-title">登录 OmniBot</div>
      <div class="card-subtitle">登录以继续使用你的私人助理</div>

      <form class="auth-form" @submit.prevent="submit">
        <!-- 邮箱 -->
        <div class="form-group">
          <div class="form-label">邮箱地址</div>
          <input
            v-model.trim="email"
            class="form-input"
            type="email"
            autocomplete="email"
            placeholder="you@example.com"
            :disabled="isSubmitting"
          />
        </div>

        <!-- 密码 -->
        <div class="form-group">
          <div class="form-label">密码</div>
          <div class="password-wrapper">
            <input
              v-model="password"
              class="form-input"
              :type="showPassword ? 'text' : 'password'"
              autocomplete="current-password"
              placeholder="输入密码"
              :disabled="isSubmitting"
            />
            <button
              type="button"
              class="password-toggle"
              title="显示/隐藏密码"
              @click="togglePassword"
            >
              <!-- 睁眼图标(showPassword=true 时显示) -->
              <svg v-if="showPassword" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24" />
                <line x1="1" y1="1" x2="23" y2="23" />
              </svg>
              <!-- 闭眼图标(默认) -->
              <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
                <circle cx="12" cy="12" r="3" />
              </svg>
            </button>
          </div>
        </div>

        <!-- 错误提示 -->
        <p v-if="errorMsg" class="error-text">{{ errorMsg }}</p>

        <!-- 登录按钮 -->
        <button
          type="submit"
          class="submit-btn"
          :disabled="isSubmitting"
        >
          {{ isSubmitting ? '登录中...' : '登录' }}
        </button>
      </form>

      <!-- 注册引导 -->
      <div class="auth-footer">
        还没有账号？<router-link to="/register" class="link">注册</router-link>
      </div>
    </div>

    <!-- 底部信息 -->
    <div class="footer">
      <div class="footer-version">{{ APP_NAME }} {{ APP_VERSION }}</div>
    </div>
  </div>
</template>

<style scoped>
.auth-page {
  width: 100%;
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  background: #fafafa;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Noto Sans SC', sans-serif;
  padding: 24px;
}

/* 品牌图标 */
.brand-icon {
  width: 40px;
  height: 40px;
  border-radius: 10px;
  background: #10a37f;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 24px;
}
.brand-icon span {
  font-size: 20px;
  font-weight: 700;
  color: #ffffff;
  line-height: 1;
}

/* 登录卡片 */
.auth-card {
  background: #ffffff;
  border-radius: 12px;
  width: 380px;
  max-width: 100%;
  padding: 32px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.06), 0 1px 2px rgba(0, 0, 0, 0.04);
}

.card-title {
  font-size: 20px;
  font-weight: 600;
  color: #171717;
  margin-bottom: 4px;
}
.card-subtitle {
  font-size: 13px;
  color: #999;
  margin-bottom: 24px;
}

.auth-form {
  display: flex;
  flex-direction: column;
}

/* 表单字段 */
.form-group + .form-group {
  margin-top: 16px;
}
.form-label {
  font-size: 13px;
  color: #666;
  font-weight: 500;
  margin-bottom: 6px;
}
.form-input {
  width: 100%;
  padding: 10px 12px;
  border: 1px solid #e5e5e5;
  border-radius: 8px;
  font-size: 14px;
  color: #171717;
  background: #ffffff;
  outline: none;
  transition: border-color 150ms ease, box-shadow 150ms ease;
  font-family: inherit;
  box-sizing: border-box;
}
.form-input::placeholder {
  color: #bbb;
}
.form-input:focus {
  border-color: #10a37f;
  box-shadow: 0 0 0 2px rgba(16, 163, 127, 0.08);
}
.form-input:disabled {
  background: #f9f9f9;
  cursor: not-allowed;
}

/* 密码框 */
.password-wrapper {
  position: relative;
}
.password-wrapper .form-input {
  padding-right: 40px;
}
.password-toggle {
  position: absolute;
  right: 8px;
  top: 50%;
  transform: translateY(-50%);
  width: 28px;
  height: 28px;
  border: none;
  background: transparent;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: background 150ms ease;
}
.password-toggle:hover {
  background: #f5f5f5;
}
.password-toggle svg {
  width: 16px;
  height: 16px;
  color: #999;
}

/* 错误提示 */
.error-text {
  margin: 12px 0 0;
  padding: 8px 12px;
  font-size: 13px;
  color: #b91c1c;
  background: #fef2f2;
  border-left: 3px solid #ef4444;
  border-radius: 4px;
}

/* 登录按钮(黑色,对齐设计稿) */
.submit-btn {
  width: 100%;
  padding: 10px;
  background: #171717;
  color: #ffffff;
  border: none;
  border-radius: 8px;
  font-size: 14px;
  font-weight: 500;
  cursor: pointer;
  transition: background 150ms ease;
  font-family: inherit;
  margin-top: 24px;
}
.submit-btn:hover:not(:disabled) {
  background: #333;
}
.submit-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

/* 注册引导 */
.auth-footer {
  text-align: center;
  font-size: 13px;
  color: #999;
  margin-top: 20px;
}
.link {
  color: #10a37f;
  text-decoration: none;
  cursor: pointer;
}
.link:hover {
  text-decoration: underline;
}

/* 底部信息 */
.footer {
  margin-top: 32px;
  text-align: center;
}
.footer-version {
  font-size: 12px;
  color: #ccc;
}
</style>
