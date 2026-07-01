<script setup lang="ts">
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import { authService } from '@/services/auth';
import { useAuthStore } from '@/stores/user';

const router = useRouter();
const auth = useAuthStore();

const email = ref('');
const password = ref('');
const confirmPassword = ref('');
const errorMsg = ref('');
const isSubmitting = ref(false);

const submit = async () => {
  if (!email.value || !password.value || !confirmPassword.value) {
    errorMsg.value = '请输入邮箱和密码';
    return;
  }
  if (password.value !== confirmPassword.value) {
    errorMsg.value = '两次输入的密码不一致';
    return;
  }
  if (password.value.length < 8 || password.value.length > 64) {
    errorMsg.value = '密码长度需为 8~64 位';
    return;
  }
  errorMsg.value = '';
  isSubmitting.value = true;
  try {
    const token = await authService.register(
      email.value,
      password.value,
      confirmPassword.value
    );
    auth.setToken(token);
    router.push('/');
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : '注册失败';
  } finally {
    isSubmitting.value = false;
  }
};
</script>

<template>
  <div class="auth-page">
    <div class="auth-card">
      <h1 class="auth-title">注册 OmniBot</h1>

      <form class="auth-form" @submit.prevent="submit">
        <label class="field">
          <span class="field-label">邮箱</span>
          <input
            v-model.trim="email"
            type="email"
            autocomplete="email"
            placeholder="you@example.com"
            :disabled="isSubmitting"
          />
        </label>

        <label class="field">
          <span class="field-label">密码</span>
          <input
            v-model="password"
            type="password"
            autocomplete="new-password"
            placeholder="8~64 位"
            :disabled="isSubmitting"
          />
        </label>

        <label class="field">
          <span class="field-label">确认密码</span>
          <input
            v-model="confirmPassword"
            type="password"
            autocomplete="new-password"
            placeholder="再次输入密码"
            :disabled="isSubmitting"
          />
        </label>

        <p v-if="errorMsg" class="error-text">{{ errorMsg }}</p>

        <button
          type="submit"
          class="submit-btn"
          :disabled="isSubmitting"
        >
          {{ isSubmitting ? '注册中...' : '注册' }}
        </button>
      </form>

      <div class="auth-footer">
        <span>已有账号?</span>
        <router-link to="/login" class="link">去登录</router-link>
      </div>
    </div>
  </div>
</template>

<style scoped>
.auth-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #f9fafb;
  padding: 24px;
}

.auth-card {
  width: 100%;
  max-width: 400px;
  padding: 40px 32px;
  background: #ffffff;
  border-radius: 12px;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.06);
}

.auth-title {
  font-size: 22px;
  font-weight: 600;
  color: #171717;
  text-align: center;
  margin: 0 0 32px;
}

.auth-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field-label {
  font-size: 13px;
  font-weight: 500;
  color: #4b5563;
}

.field input {
  padding: 10px 12px;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  font-size: 14px;
  color: #171717;
  outline: none;
  transition: border-color 0.15s;
}

.field input:focus {
  border-color: #10a37f;
}

.field input:disabled {
  background: #f3f4f6;
  cursor: not-allowed;
}

.error-text {
  margin: -4px 0 0;
  padding: 8px 12px;
  font-size: 13px;
  color: #b91c1c;
  background: #fef2f2;
  border-left: 3px solid #ef4444;
  border-radius: 4px;
}

.submit-btn {
  margin-top: 8px;
  padding: 11px 16px;
  background: #10a37f;
  color: #ffffff;
  font-size: 14px;
  font-weight: 500;
  border: none;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s;
}

.submit-btn:hover:not(:disabled) {
  background: #0d8f6f;
}

.submit-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.auth-footer {
  margin-top: 24px;
  font-size: 13px;
  color: #6b7280;
  text-align: center;
}

.link {
  margin-left: 6px;
  color: #10a37f;
  text-decoration: none;
  font-weight: 500;
}

.link:hover {
  text-decoration: underline;
}
</style>
