/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import path from 'node:path';

// M8 性能修复（P2.1）：主 chunk 976kB 过大。把 markdown 渲染链（react-markdown
// 的 unified/micromark/mdast/hast 生态）与 shiki 代码高亮链拆为独立 chunk，
// 其余 node_modules 依赖拆入公共 vendor chunk，降低主入口体积。
const markdownPkgs = [
  'react-markdown',
  'remark-gfm',
  'remark-parse',
  'remark-rehype',
  'remark-stringify',
  'unified',
  'bail',
  'trough',
  'devlop',
  'zwitch',
  'vfile',
  'vfile-message',
];
// 前缀族：micromark*/mdast*/hast-util*/unist-util*/character-entities* 等渲染链包；
// 其余 unified 生态零散依赖必须全部纳入，否则会落进 vendor 造成 chunk 循环依赖
// （实测：stringify-entities/parse-entities/space-separated-tokens 等漏分即触发
// "Circular chunk: markdown -> vendor -> markdown"）。
const markdownPrefixes = [
  'micromark',
  'mdast-',
  'hast-util',
  'hastscript',
  'unist-',
  'character-entities',
  'character-reference',
  'decode-named-character-reference',
  'parse-entities',
  'stringify-entities',
  'property-information',
  'comma-separated-tokens',
  'space-separated-tokens',
  'html-url-attributes',
  'estree-util-',
  'estree-walker',
  'style-to-object',
  'is-decimal',
  'is-hexadecimal',
  'web-namespaces',
  'longest-streak',
];
const shikiPrefixes = ['shiki', '@shikijs'];

// pkgOf 从模块路径提取 node_modules 下的顶层包名（兼容 @scope/pkg）。
function pkgOf(id: string): string | null {
  const m = id.match(/[\\/]node_modules[\\/]((?:@[^\\/]+[\\/])?[^\\/]+)/);
  return m ? m[1] : null;
}

function isMarkdownPkg(pkg: string): boolean {
  return markdownPkgs.includes(pkg) || markdownPrefixes.some((p) => pkg.startsWith(p));
}

function isShikiPkg(pkg: string): boolean {
  return shikiPrefixes.some((p) => pkg.startsWith(p));
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks(id: string): string | undefined {
          const pkg = pkgOf(id);
          if (!pkg) return undefined;
          if (isShikiPkg(pkg)) return 'shiki';
          if (isMarkdownPkg(pkg)) return 'markdown';
          return 'vendor';
        },
      },
    },
  },
  test: {
    environment: 'node',
    globals: true,
    include: ['src/**/*.test.ts'],
  },
  server: {
    proxy: {
      // 后端 Gin 默认 8080；前端统一用相对路径 /api/v1，绕开 CORS
      // SSE 经 Vite http-proxy 默认对流式响应直通，无需额外缓冲配置
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
});
