import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright e2e（阶段 E）：五步冒烟（登录→建库→上传→搜索→聊天）。
 * 运行前置：后端已在 :8080（bin/woodpecker-e2e.exe --mode=all）+ 本地账号；
 * 凭证经环境变量注入（不进仓库）：E2E_USERNAME / E2E_PASSWORD。
 * 开发机运行：npm run test:e2e（webServer 复用已在跑的 vite，未跑则自动拉起）。
 */
export default defineConfig({
  testDir: './e2e',
  // serial：五个步骤共享一次登录态与同一个新建知识库，禁止并行打乱顺序
  fullyParallel: false,
  timeout: 120_000,
  expect: { timeout: 10_000 },
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:5173',
    trace: 'retain-on-failure',
    // Windows 实测：headless（含完整 Chromium 新 headless）的 Browser.close 会挂死；
    // 有头模式收尾正常——本机 e2e 走 headful（CI 沙箱不受影响）
    headless: false,
    viewport: { width: 1280, height: 800 },
  },
  webServer: {
    command: 'npm run dev',
    url: 'http://localhost:5173',
    reuseExistingServer: true,
    timeout: 60_000,
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        // Windows 实测：playwright 自带 chromium（headless shell 与完整版）的
        // worker 都会中途挂死且 timeout 失效；本机 Chrome 频道稳定（同 browser_use）
        channel: 'chrome',
      },
    },
  ],
});
