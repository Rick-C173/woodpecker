import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App } from './App';
import { ThemeProvider } from './components/theme/theme-provider';
import { PaletteProvider } from './components/theme/palette-provider';
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
        </PaletteProvider>
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>,
);
