import { defineConfig } from '@playwright/test';

// Тести виконуються проти вже запущеного сервера «Догляд».
// Запустіть його командою ./start.ps1 (Windows) або ./start.sh (Linux, macOS),
// після чого — npm run e2e.
//
// CHROMIUM_PATH дозволяє використати вже встановлений браузер замість завантаженого Playwright.
const executablePath = process.env.CHROMIUM_PATH;

export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [['list']],
  // Паралельний запуск вимкнено: тести працюють з однією базою даних.
  workers: 1,
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8080',
    locale: 'uk-UA',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    launchOptions: executablePath ? { executablePath } : {},
  },
});
