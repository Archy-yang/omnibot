<script setup lang="ts">
import { computed } from 'vue';
import type { MarkdownRendererProps } from '@/types/components';

const props = defineProps<MarkdownRendererProps>();

// XSS 防护：转义 HTML 特殊字符
const escapeHtml = (text: string): string => {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
};

// 简单的 Markdown 解析
const parseMarkdown = (content: string): string => {
  let html = escapeHtml(content);

  // 代码块
  html = html.replace(
    /```(\w+)?\n([\s\S]*?)```/g,
    (_, _lang, code) => `
      <pre class="bg-gray-900 text-gray-100 p-4 rounded-lg overflow-x-auto my-4">
        <code class="text-sm font-mono">${code.trim()}</code>
      </pre>
    `
  );

  // 行内代码
  html = html.replace(
    /`([^`]+)`/g,
    (_, code) => `
      <code class="inline-code">
        ${code}
      </code>
    `
  );

  // 标题
  html = html.replace(/^#\s+(.*)$/gm, '<h1 class="text-2xl font-bold my-4">$1</h1>');
  html = html.replace(/^##\s+(.*)$/gm, '<h2 class="text-xl font-bold my-3">$1</h2>');
  html = html.replace(/^###\s+(.*)$/gm, '<h3 class="text-lg font-semibold my-2">$1</h3>');

  // 粗体
  html = html.replace(/\*\*([^*]+)\*\*/g, '<strong class="font-semibold">$1</strong>');

  // 斜体
  html = html.replace(/\*([^*]+)\*/g, '<em class="italic">$1</em>');

  // 链接
  html = html.replace(
    /\[([^\]]+)\]\(([^)]+)\)/g,
    '<a href="$2" class="text-blue-600 dark:text-blue-400 hover:underline" target="_blank" rel="noopener noreferrer">$1</a>'
  );

  // 无序列表
  html = html.replace(/^- (.*)$/gm, '<li class="ml-4">$1</li>');
  html = html.replace(/((?:<li.*<\/li>\n?)+)/g, '<ul class="list-disc my-4">$1</ul>');

  // 有序列表
  html = html.replace(/^\d+\. (.*)$/gm, '<li class="ml-4">$1</li>');
  html = html.replace(/((?:<li.*<\/li>\n?)+)/g, '<ol class="list-decimal my-4">$1</ol>');

  // 表格行
  html = html.replace(/^\|(.+)\|$/gm, (match, content) => {
    const cells = content.split('|').map((cell: string) => cell.trim());
    if (cells.every((cell: string) => /^-+$/.test(cell))) {
      return ''; // 分隔行
    }
    const tag = match.includes('---') ? 'th' : 'td';
    return `<tr>${cells.map((cell: string) => `<${tag} class="border border-gray-300 dark:border-gray-600 px-4 py-2">${cell}</${tag}>`).join('')}</tr>`;
  });

  // 表格包裹
  html = html.replace(/((?:<tr>.*<\/tr>\n?)+)/g, '<table class="border-collapse w-full my-4">$1</table>');

  // 换行转 <br>
  html = html.replace(/\n/g, '<br>');

  // 段落
  html = html.replace(/^(?!<[hulo<p])(.+)$/gm, '<p class="my-2">$1</p>');

  return html;
};

const renderedContent = computed(() => parseMarkdown(props.content));
</script>

<template>
  <div
    class="markdown-renderer text-gray-800 dark:text-gray-200 leading-relaxed"
    v-html="renderedContent"
  />
</template>

<style scoped>
/* Use regular CSS instead of @apply for Tailwind v4 compatibility */
.markdown-renderer :deep(table) {
  border-collapse: collapse;
  width: 100%;
  margin-top: 1rem;
  margin-bottom: 1rem;
}

.markdown-renderer :deep(th),
.markdown-renderer :deep(td) {
  border: 1px solid #d1d5db;
  padding: 0.5rem 1rem;
}

.dark .markdown-renderer :deep(th),
.dark .markdown-renderer :deep(td) {
  border-color: #4b5563;
}

.markdown-renderer :deep(th) {
  background-color: #f3f4f6;
  font-weight: 600;
}

.dark .markdown-renderer :deep(th) {
  background-color: #1f2937;
}

.markdown-renderer :deep(ul),
.markdown-renderer :deep(ol) {
  margin-top: 1rem;
  margin-bottom: 1rem;
  padding-left: 2rem;
}

.markdown-renderer :deep(li) {
  margin-top: 0.25rem;
  margin-bottom: 0.25rem;
}

.markdown-renderer :deep(pre) {
  background-color: #111827;
  color: #f3f4f6;
  padding: 1rem;
  border-radius: 0.5rem;
  overflow-x: auto;
  margin-top: 1rem;
  margin-bottom: 1rem;
}

.markdown-renderer :deep(code) {
  font-size: 0.875rem;
  font-family: ui-monospace, Consolas, monospace;
}

.markdown-renderer :deep(.inline-code) {
  background-color: #f3f4f6;
  padding: 0.125rem 0.375rem;
  border-radius: 0.25rem;
  color: #db2777;
  font-size: 0.875rem;
  font-family: ui-monospace, Consolas, monospace;
}

.dark .markdown-renderer :deep(.inline-code) {
  background-color: #1f2937;
  color: #f472b6;
}

.markdown-renderer :deep(a) {
  color: #2563eb;
}

.dark .markdown-renderer :deep(a) {
  color: #60a5fa;
}

.markdown-renderer :deep(a:hover) {
  text-decoration: underline;
}

.markdown-renderer :deep(h1) {
  font-size: 1.5rem;
  font-weight: 700;
  margin-top: 1rem;
  margin-bottom: 1rem;
}

.markdown-renderer :deep(h2) {
  font-size: 1.25rem;
  font-weight: 700;
  margin-top: 0.75rem;
  margin-bottom: 0.75rem;
}

.markdown-renderer :deep(h3) {
  font-size: 1.125rem;
  font-weight: 600;
  margin-top: 0.5rem;
  margin-bottom: 0.5rem;
}

.markdown-renderer :deep(p) {
  margin-top: 0.5rem;
  margin-bottom: 0.5rem;
}
</style>
