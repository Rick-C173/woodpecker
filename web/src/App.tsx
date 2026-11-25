import { RouterProvider } from 'react-router-dom';
import { router } from './router';

/** 根布局挂载（router 定义所在，见 src/router.tsx）。 */
export function App() {
  return <RouterProvider router={router} />;
}
