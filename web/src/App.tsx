import { RouterProvider } from 'react-router-dom';
import { router } from './router';

// TODO(方向待办-3): 前端体验批——导航结构/操作反馈/空态等快赢项先行，MCP Server 落地后
// 做一次结构性打磨（避免返工两遍）。见 md/前端体验改进方案.md。（2026-11-26 规划）
// 重构时一并落地：API Token 管理页 /settings/tokens（后端 M15 已就绪，契约见 docs/swagger.json，
// 跑 npm run gen:api 生成类型）。

/** 根布局挂载（router 定义所在，见 src/router.tsx）。 */
export function App() {
  return <RouterProvider router={router} />;
}
