import { RouterProvider } from 'react-router-dom';
import { router } from './router';

// TODO(方向待办-3): 前端整体重构规划已定稿（md/前端整体重构规划.md，2026-11-27）——
// 阶段 0 安全网 → 阶段 1 公共层统一 → 阶段 2 巨页拆分 → 阶段 3 功能补齐（含本注记提到的
// API Token 管理页 /settings/tokens，跑 npm run gen:api 生成类型）→ 阶段 4 打磨收尾。
// 早期快赢项见 md/前端体验改进方案.md（P0/P1/P2 已并入上述规划的对应阶段）。

/** 根布局挂载（router 定义所在，见 src/router.tsx）。 */
export function App() {
  return <RouterProvider router={router} />;
}
