/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./index.html",
    "./src/**/*.{vue,js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        'dark-bg': '#0F1115',
        'sidebar': '#16191E',
        'card': '#1E2228',
        'accent-blue': '#3B82F6',
        'accent-teal': '#14B8A6',
      },
    },
  },
  plugins: [],
}
