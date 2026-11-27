/**
 * 五步冒烟（阶段 E 验收）：登录 → 建库 → 上传摄取 → 检索命中 → SSE 问答。
 * 合并为单个 test 一次跑完——Windows 实测 serial 多测试在间隙 teardown
 * （context 重建）处挂死，单测试无此边界；凭证经 E2E_USERNAME/E2E_PASSWORD
 * 注入（密码不进仓库）。摄取/问答依赖后端模型服务就绪，失败信息带状态徽标。
 */

import { expect, test } from '@playwright/test';

const USERNAME = process.env.E2E_USERNAME ?? 'root';
const PASSWORD = process.env.E2E_PASSWORD ?? '';

test.skip(!PASSWORD, '需要 E2E_PASSWORD（本地 e2e 环境账号，经环境变量注入）');

test('五步冒烟：登录 → 建库 → 上传 → 搜索 → 聊天', async ({ page }) => {
  test.setTimeout(420_000); // 摄取与问答依赖模型服务，放宽全局 120s
  // ① 登录 → /kbs
  await page.goto('/login');
  await page.getByPlaceholder('用户名或邮箱').fill(USERNAME);
  await page.getByPlaceholder('密码').fill(PASSWORD);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/kbs$/);
  await expect(page.getByRole('link', { name: '知识库', exact: true })).toBeVisible();
  await expect(page.getByTestId('command-palette-trigger')).toBeVisible();

  // ② 建库 → 详情
  const kbName = `e2e-${Date.now()}`;
  await page.getByRole('button', { name: '新建知识库' }).click();
  await page.getByLabel('名称 *').fill(kbName);
  await page.getByRole('button', { name: '创建', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeHidden();
  await page.getByText(kbName).first().click();
  const kbUrl = page.url();
  expect(kbUrl).toMatch(/\/kbs\/\d+$/);
  await expect(page.getByRole('heading', { name: kbName })).toBeVisible();

  // ③ 上传 sample.md → 摄取至已就绪（异步 worker + 模型服务）
  await page.getByRole('button', { name: '接入知识源' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('button', { name: '上传文件' }).click();
  await dialog.locator('input[type="file"]').setInputFiles('e2e/fixtures/sample.md');
  await dialog.getByRole('button', { name: /上传 1 个文件/ }).click();
  // 「失败」先出现即摄取失败（多为模型服务未配置）；3 分钟内应见「已就绪」
  await expect(page.getByText('失败').first()).toBeHidden({ timeout: 180_000 });
  await expect(page.getByText('已就绪').first()).toBeVisible({ timeout: 180_000 });

  // ④ 检索「啄木鸟冒烟」命中
  await page.goto(`${kbUrl}/search`);
  await page.getByPlaceholder('输入关键词检索（如：限流实现）').fill('啄木鸟冒烟');
  await expect(page.getByTestId('search-result-card').first()).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText('共', { exact: false })).toContainText('1');

  // ⑤ 问答（SSE 流式，带引用）
  await page.goto(`${kbUrl}/chat`);
  await page.getByTestId('new-conversation').click();
  await expect(page).toHaveURL(/\/chat\/\d+$/);
  await page.getByTestId('chat-input').fill('这份文档的关键词是什么？');
  await page.getByTestId('chat-send').click();
  await expect(page.getByTestId('message-assistant-pending')).toBeVisible({ timeout: 60_000 });
  await expect(page.getByTestId('message-assistant-pending')).toBeHidden({ timeout: 180_000 });
  await expect(page.getByTestId('chat-error-card')).toHaveCount(0);
  await expect(page.getByTestId('chat-window')).toContainText('啄木鸟冒烟');
});
