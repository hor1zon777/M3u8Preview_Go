/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: [
    './index.html',
    './src/**/*.{js,ts,jsx,tsx}',
  ],
  theme: {
    extend: {
      colors: {
        emby: {
          green: '#52B54B',
          'green-dark': '#3E8437',
          'green-light': '#5EC157',
          'green-hover': '#62C95B',
          'bg-deep': '#080808',
          'bg-base': '#101010',
          'bg-card': '#181818',
          'bg-input': '#242424',
          'bg-hover': '#252528',
          'bg-elevated': '#282828',
          'border': '#262626',
          'border-light': '#333333',
          'bg-dialog': '#121212',
          'border-subtle': '#1F1F1F',
          'text-primary': '#E0E0E0',
          'text-secondary': '#999999',
          'text-muted': '#666666',
        },
      },
    },
  },
  plugins: [],
};
