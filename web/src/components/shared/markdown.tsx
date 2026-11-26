/**
 * Markdown 渲染组件（架构 §1.5）：
 * - react-markdown v9 + remark-gfm；表格样式对齐站内卡片；外链新窗口。
 * - shiki fine-grained bundle（createHighlighterCore + JS 正则引擎，无 WASM），
 *   白名单语言静态注册（go/ts/tsx/js/py/java/sql/yaml/json/bash/md/css/html），
 *   其余语言降级纯文本 pre；明暗两套主题（github-light/dark），随 html[data-theme]
 *   经 CSS 变量切换（不随 5 套配色联动，§5.4 第 6 条）。
 * - 流式期间（streaming=true）代码块不高亮（纯 pre），done 后整段一次性高亮。
 * - 行内 [n] 引用编号渲染为可点击徽标（onCite 打开引用抽屉）。
 * - shiki 输出经 codeToHtml 生成、代码文本已转义，为标准受信管道（dangerouslySetInnerHTML
 *   仅注入 shiki 高亮 HTML，不注入任何用户/LLM 原始 HTML）。
 */

import {
  Children,
  cloneElement,
  isValidElement,
  useEffect,
  useState,
  type ReactNode,
} from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { HighlighterCore } from 'shiki/core';
import { splitInlineCitations } from '@/lib/chat-events';
import { cn } from '@/lib/utils';

/** 高亮白名单（PRD R1 全部支持格式 + 前端常用；其余语言降级纯文本）。 */
const WHITELIST_LANGS = new Set([
  'go',
  'typescript',
  'ts',
  'tsx',
  'javascript',
  'js',
  'python',
  'py',
  'java',
  'sql',
  'yaml',
  'yml',
  'json',
  'bash',
  'sh',
  'shell',
  'markdown',
  'md',
  'css',
  'html',
]);

/** 语言别名归一（shiki grammar id）。 */
function normalizeLang(lang: string): string {
  switch (lang) {
    case 'ts':
      return 'typescript';
    case 'js':
      return 'javascript';
    case 'py':
      return 'python';
    case 'yml':
      return 'yaml';
    case 'sh':
    case 'shell':
      return 'bash';
    case 'md':
      return 'markdown';
    default:
      return lang;
  }
}

let highlighterPromise: Promise<HighlighterCore> | null = null;

/** 模块级单例：白名单语言 + 明暗双主题静态注册，一次加载全局复用。 */
function getHighlighter(): Promise<HighlighterCore> {
  if (!highlighterPromise) {
    highlighterPromise = import('shiki/core').then(({ createHighlighterCore }) =>
      createHighlighterCore({
        themes: [
          import('shiki/themes/github-light.mjs'),
          import('shiki/themes/github-dark.mjs'),
        ],
        langs: [
          import('shiki/langs/go.mjs'),
          import('shiki/langs/typescript.mjs'),
          import('shiki/langs/tsx.mjs'),
          import('shiki/langs/javascript.mjs'),
          import('shiki/langs/python.mjs'),
          import('shiki/langs/java.mjs'),
          import('shiki/langs/sql.mjs'),
          import('shiki/langs/yaml.mjs'),
          import('shiki/langs/json.mjs'),
          import('shiki/langs/bash.mjs'),
          import('shiki/langs/markdown.mjs'),
          import('shiki/langs/css.mjs'),
          import('shiki/langs/html.mjs'),
        ],
        engine: import('shiki/engine/javascript').then((m) => m.createJavaScriptRegexEngine()),
      }),
    );
  }
  return highlighterPromise;
}

/** 生成双主题高亮 HTML（defaultColor=false → 输出 --shiki-light/--shiki-dark 变量，CSS 切换）。 */
function highlightToHtml(hl: HighlighterCore, code: string, lang: string): string | null {
  try {
    return hl.codeToHtml(code, {
      lang: normalizeLang(lang),
      themes: { light: 'github-light', dark: 'github-dark' },
      defaultColor: false,
    });
  } catch {
    return null; // 未知语言/非法输入降级纯文本
  }
}

/** 流式/降级用的纯 pre 代码块（带语言标签）。 */
function PlainCodeBlock({ lang, code }: { lang: string; code: string }) {
  return (
    <div className="codeblock">
      {lang ? (
        <span className="absolute right-2 top-1.5 font-mono text-[10px] text-[var(--faint)]">
          {lang}
        </span>
      ) : null}
      <pre className="overflow-auto px-3 py-2.5 font-mono text-xs leading-relaxed text-[var(--code-fg)]">
        <code>{code}</code>
      </pre>
    </div>
  );
}

interface CodeBlockProps {
  lang: string;
  code: string;
  streaming: boolean;
  highlighter: HighlighterCore | null;
}

/** 代码块：流式期间纯 pre；done 后一次性 shiki 高亮（白名单语言，其余降级纯文本）。 */
function ShikiCodeBlock({ lang, code, streaming, highlighter }: CodeBlockProps) {
  if (
    streaming ||
    !highlighter ||
    !WHITELIST_LANGS.has(lang.toLowerCase())
  ) {
    return <PlainCodeBlock lang={lang} code={code} />;
  }
  const html = highlightToHtml(highlighter, code, lang);
  if (!html) return <PlainCodeBlock lang={lang} code={code} />;
  return <div className="codeblock" dangerouslySetInnerHTML={{ __html: html }} />;
}

/** 行内引用徽标：点击打开引用抽屉并聚焦对应条目。 */
function CiteBadge({ seq, onCite }: { seq: number; onCite?: (seq: number) => void }) {
  return (
    <button
      type="button"
      className="mx-0.5 inline-flex h-[18px] min-w-[18px] cursor-pointer items-center justify-center rounded-full border border-[var(--border-strong)] bg-[var(--primary-soft)] px-1 align-[1px] font-mono text-[10px] font-semibold leading-none text-[var(--primary)] transition-colors hover:bg-[var(--accent-soft)] hover:text-[var(--accent)]"
      title={`查看引用 [${seq}]`}
      onClick={(e) => {
        e.preventDefault();
        e.stopPropagation();
        onCite?.(seq);
      }}
    >
      {seq}
    </button>
  );
}

/** 递归把字符串节点中的 [n] 替换为引用徽标（p/li/td 等块内文本）。 */
function decorateInlineCitations(
  children: ReactNode,
  onCite?: (seq: number) => void,
): ReactNode {
  if (!onCite) return children;
  return Children.map(children, (child) => {
    if (typeof child === 'string') {
      const segments = splitInlineCitations(child);
      if (segments.length === 0) return null;
      if (segments.length === 1 && segments[0].type === 'text') return child;
      return segments.map((seg, i) =>
        seg.type === 'cite' && seg.seq !== null ? (
          <CiteBadge key={i} seq={seg.seq} onCite={onCite} />
        ) : (
          <span key={i}>{seg.value}</span>
        ),
      );
    }
    if (isValidElement(child)) {
      const props = child.props as { children?: ReactNode };
      if (props.children !== undefined && typeof props.children !== 'string') {
        return cloneElement(child, undefined, decorateInlineCitations(props.children, onCite));
      }
    }
    return child;
  });
}

export interface MarkdownProps {
  content: string;
  /** 流式期间：代码块纯 pre 不高亮（§1.5 增量渲染策略） */
  streaming?: boolean;
  /** 行内 [n] 引用徽标点击回调（未传则不渲染徽标） */
  onCite?: (seq: number) => void;
  className?: string;
}

/** 站内统一 Markdown 渲染（回答正文 / 引用 snippet 预览复用）。 */
export function Markdown({ content, streaming = false, onCite, className }: MarkdownProps) {
  const [highlighter, setHighlighter] = useState<HighlighterCore | null>(null);

  // 挂载后异步加载 shiki；加载完成前代码块以纯 pre 渲染（无闪烁风险：仅样式切换）
  useEffect(() => {
    let cancelled = false;
    getHighlighter()
      .then((hl) => {
        if (!cancelled) setHighlighter(hl);
      })
      .catch(() => {
        /* 高亮加载失败：永久降级纯文本代码块 */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className={cn('text-sm leading-relaxed', className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          pre: ({ children }) => {
            // block code 由 code 组件整体接管；pre 仅兜底包裹非代码内容
            const child = Array.isArray(children) ? children[0] : children;
            if (isValidElement(child)) {
              return <>{child}</>;
            }
            return <div className="codeblock"><pre className="overflow-auto px-3 py-2.5 font-mono text-xs">{children}</pre></div>;
          },
          code: ({ className: cls, children }) => {
            const match = /language-([\w+-]+)/.exec(cls ?? '');
            if (!match) {
              return (
                <code className="rounded-[4px] bg-[var(--code-bg)] px-1 py-0.5 font-mono text-[13px] text-[var(--code-fg)]">
                  {children}
                </code>
              );
            }
            const code = String(Array.isArray(children) ? children.join('') : children).replace(
              /\n$/,
              '',
            );
            return (
              <ShikiCodeBlock
                lang={match[1]}
                code={code}
                streaming={streaming}
                highlighter={highlighter}
              />
            );
          },
          a: ({ href, children }) => (
            <a
              href={href}
              target="_blank"
              rel="noreferrer noopener"
              className="text-[var(--primary)] underline underline-offset-2 hover:text-[var(--accent)]"
            >
              {children}
            </a>
          ),
          table: ({ children }) => (
            <div className="my-2 overflow-auto rounded-[var(--radius)] border border-[var(--border)]">
              <table className="w-full border-collapse text-xs [&_td]:border-[var(--border)] [&_td]:px-2.5 [&_td]:py-1.5 [&_th]:border-[var(--border)] [&_th]:bg-[var(--primary-soft)] [&_th]:px-2.5 [&_th]:py-1.5 [&_th]:text-left [&_th]:font-semibold [&_tbody_tr:last-child]:border-b-0 [&_tr]:border-b [&_tr]:border-[var(--border)]">
                {children}
              </table>
            </div>
          ),
          p: ({ children }) => <p className="my-1.5 first:mt-0 last:mb-0">{decorateInlineCitations(children, onCite)}</p>,
          li: ({ children }) => <li className="ml-5 list-disc">{decorateInlineCitations(children, onCite)}</li>,
          h1: ({ children }) => <h1 className="mb-2 mt-3 text-base font-semibold first:mt-0">{children}</h1>,
          h2: ({ children }) => <h2 className="mb-2 mt-3 text-[15px] font-semibold first:mt-0">{children}</h2>,
          h3: ({ children }) => <h3 className="mb-1.5 mt-2.5 text-sm font-semibold first:mt-0">{children}</h3>,
          blockquote: ({ children }) => (
            <blockquote className="my-2 border-l-2 border-[var(--border-strong)] pl-3 text-[var(--muted)]">
              {children}
            </blockquote>
          ),
          ul: ({ children }) => <ul className="my-1.5 space-y-1">{children}</ul>,
          ol: ({ children }) => <ol className="my-1.5 list-decimal space-y-1">{children}</ol>,
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
