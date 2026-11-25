/**
 * 检索页（F4）：GET /kbs/{kb}/search?q=&page=。
 * 防抖 300ms + q 为空不发请求（enabled）；结果片段卡（file_path + line_start-end +
 * content + score）+ 分页；无命中 EmptyState「未找到相关内容，换个说法试试」。
 */

import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { SearchX, Search as SearchIcon } from 'lucide-react';
import { api } from '@/api/client';
import { qk } from '@/lib/queryKeys';
import { useDebouncedValue } from '@/hooks/use-debounced-value';
import { QueryBoundary } from '@/components/states/query-boundary';
import { EmptyState } from '@/components/states/empty-state';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

interface SearchPageResult {
  items: {
    chunk_id?: number;
    document_id?: number;
    file_path?: string | null;
    heading_path?: string | null;
    line_start?: number | null;
    line_end?: number | null;
    content?: string | null;
    score?: number | null;
  }[];
  total: number;
  page: number;
  page_size: number;
}

export function KbSearchPage() {
  const { kbId } = useParams();
  const kbIdNum = Number(kbId);

  const [input, setInput] = useState('');
  const [page, setPage] = useState(1);
  // 防抖 300ms：q 停止变化后才发起请求
  const q = useDebouncedValue(input.trim(), 300);

  const search = useQuery({
    queryKey: qk.kbs.search(kbIdNum, q),
    queryFn: async (): Promise<SearchPageResult> => {
      const { data } = await api.GET('/kbs/{kb}/search', {
        params: { path: { kb: kbIdNum }, query: { q, page } },
      });
      return {
        items: data?.items ?? [],
        total: data?.total ?? 0,
        page: data?.page ?? 1,
        page_size: data?.page_size ?? 20,
      };
    },
    enabled: q.length > 0 && Number.isFinite(kbIdNum) && kbIdNum > 0,
    staleTime: 0,
    placeholderData: keepPreviousData,
  });

  const totalPages = Math.max(
    1,
    Math.ceil((search.data?.total ?? 0) / (search.data?.page_size || 20)),
  );

  return (
    <div className="mx-auto w-full max-w-5xl px-6 py-8">
      <h1 className="mb-1 text-lg font-semibold">内容检索</h1>
      <p className="mb-4 text-sm text-[var(--muted)]">
        混合检索（向量 + 全文）+ 精排，命中片段含文件路径与行号，可跳转到对应文档。
      </p>

      <div className="relative mb-6">
        <SearchIcon
          className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[var(--faint)]"
          aria-hidden="true"
        />
        <Input
          className="pl-9"
          placeholder="输入关键词检索（如：限流实现）"
          value={input}
          aria-label="检索关键词"
          onChange={(e) => {
            setInput(e.target.value);
            setPage(1);
          }}
        />
      </div>

      {q.length === 0 ? (
        <EmptyState
          icon={SearchIcon}
          title="输入关键词开始检索"
          description="支持检索代码、Markdown 与网页内容片段。"
        />
      ) : (
        <QueryBoundary
          query={search}
          skeleton={
            <div className="space-y-3">
              {Array.from({ length: 3 }, (_, i) => (
                <Skeleton key={i} className="h-28 w-full" />
              ))}
            </div>
          }
          empty={
            <EmptyState
              icon={SearchX}
              title="未找到相关内容"
              description="换个说法试试，或检查关键词拼写。"
            />
          }
        >
          {(data) => (
            <>
              <p className="mb-3 text-xs text-[var(--faint)]">共 {data.total} 条命中</p>
              <div className="space-y-3">
                {data.items.map((item) => (
                  <article
                    key={item.chunk_id}
                    className="kb-card p-4"
                    data-testid="search-result-card"
                  >
                    <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                      <div className="min-w-0 font-mono text-xs text-[var(--muted)]">
                        {item.file_path ? (
                          <>
                            <Link
                              to={`/kbs/${kbIdNum}/documents/${item.document_id}`}
                              className="block truncate text-[var(--primary)] hover:underline"
                            >
                              {item.file_path}
                            </Link>
                            {item.line_start ? (
                              <span>
                                :{item.line_start}
                                {item.line_end && item.line_end !== item.line_start
                                  ? `-${item.line_end}`
                                  : ''}
                              </span>
                            ) : null}
                          </>
                        ) : (
                          <span>{item.heading_path ?? '网页片段'}</span>
                        )}
                      </div>
                      {item.score !== undefined && item.score !== null ? (
                        <span
                          className="rounded-md bg-[var(--muted)] px-1.5 py-0.5 text-xs tabular-nums text-[var(--muted-foreground)]"
                          title="融合得分（RRF，越小越相关）"
                        >
                          score {item.score.toFixed(4)}
                        </span>
                      ) : null}
                    </div>
                    <pre className="codeblock max-h-48 overflow-auto whitespace-pre-wrap p-3 font-mono text-xs leading-relaxed text-[var(--code-fg)]">
                      {item.content}
                    </pre>
                    {item.document_id ? (
                      <p className="mt-2">
                        <Link
                          to={`/kbs/${kbIdNum}/documents/${item.document_id}`}
                          className="text-xs text-[var(--primary)] hover:underline"
                        >
                          查看来源文档 →
                        </Link>
                      </p>
                    ) : null}
                  </article>
                ))}
              </div>

              {data.total > data.page_size ? (
                <div className="mt-4 flex items-center justify-end gap-2 text-sm text-[var(--muted)]">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={data.page <= 1}
                    onClick={() => setPage((p) => Math.max(1, p - 1))}
                  >
                    上一页
                  </Button>
                  <span className="tabular-nums">
                    第 {data.page} / {totalPages} 页
                  </span>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={data.page >= Math.ceil(data.total / data.page_size)}
                    onClick={() => setPage((p) => p + 1)}
                  >
                    下一页
                  </Button>
                </div>
              ) : null}
            </>
          )}
        </QueryBoundary>
      )}
    </div>
  );
}
