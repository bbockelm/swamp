/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    './src/pages/**/*.{js,ts,jsx,tsx,mdx}',
    './src/components/**/*.{js,ts,jsx,tsx,mdx}',
    './src/app/**/*.{js,ts,jsx,tsx,mdx}',
    './src/lib/**/*.{js,ts,jsx,tsx,mdx}',
  ],
  theme: {
    extend: {
      colors: {
        brand: {
          50: '#f0f9ec',
          100: '#d8f0cc',
          200: '#b5e09e',
          300: '#8dcc6c',
          400: '#6aad4a',
          500: '#5a9e3e',
          600: '#4d8b31',
          700: '#3f7228',
          800: '#335b21',
          900: '#284a1c',
          950: '#1a3612',
        },
        navy: {
          700: '#1e3050',
          800: '#1a2744',
          900: '#162036',
          950: '#0f1724',
        },
      },
    },
  },
  plugins: [],
};
