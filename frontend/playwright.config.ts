import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  workers: process.env.CI ? 2 : undefined,
  // Tests run without animations so that clicks and color checks never wait for one.
  // The motion tests turn them back on.
  use: {
    baseURL: 'http://127.0.0.1:4173', locale: 'en-US', timezoneId: 'UTC', trace: 'retain-on-failure',
    contextOptions: { reducedMotion: 'reduce' },
  },
  webServer: { command: 'npm run dev -- --host 127.0.0.1 --port 4173 --strictPort', url: 'http://127.0.0.1:4173', reuseExistingServer: !process.env.CI },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    { name: 'firefox', use: { ...devices['Desktop Firefox'] } },
    { name: 'webkit', use: { ...devices['Desktop Safari'] } },
  ],
})
