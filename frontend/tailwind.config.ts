import type { Config } from 'tailwindcss'

export default {
  content: [
    './index.html',
    './src/**/*.{vue,ts,tsx,js,jsx}',
  ],
  theme: {
    extend: {},
  },
  plugins: [],
} satisfies Config
