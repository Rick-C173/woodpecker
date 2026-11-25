import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App } from './App';
import { ThemeProvider } from './components/theme/theme-provider';
import { PaletteProvider } from './components/theme/palette-provider';
import { Toaster } from './components/ui/sonner';
import './index.css';

// TanStack Query：唯一服务端状态层（staleTime 分策略写在各 hook，见架构 §1.2）
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

createRoot(document.getElementById('root') as HTMLElement).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <PaletteProvider>
          <App />
          {/* toast（sonner）：操作失败/成功提示，主题对齐 ThemeProvider */}
          <Toaster position="top-center" richColors />
        </PaletteProvider>
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>,
);
