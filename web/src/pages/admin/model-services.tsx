/**
 * 模型服务配置（FR-12，/admin/models）：服务/条目两级管理。
 * - 服务列表：名称 / Base URL / enabled 徽标 / has_key 图标 / 条目数；新建/编辑
 *   dialog（key 字段 placeholder「留空不修改」）；启停（MODEL_004 toast）；删除
 *   （MODEL_003 toast）；测试连接 → POST test → 三用途结果徽标（ok 绿 / err 红+详情）。
 * - 条目管理子区：按服务/用途筛选列表（用途徽标、模型名、参数、默认徽标）；
 *   创建/编辑/删除（MODEL_002 删默认拦截 toast）；设默认（is_default:true）。
 */

import { useState } from 'react';
import { toast } from 'sonner';
import { CircleAlert, CircleCheck, KeyRound, LoaderCircle, Plus, Wrench } from 'lucide-react';
import { QueryBoundary } from '@/components/states/query-boundary';
import { EmptyState } from '@/components/states/empty-state';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ConfirmDialog } from '@/components/shared/confirm-dialog';
import {
  useCreateModelEntry,
  useCreateModelService,
  useDeleteModelEntry,
  useDeleteModelService,
  useModelEntries,
  useModelServices,
  usePatchModelEntry,
  usePatchModelService,
  useTestModelService,
  type ModelTestResult,
} from '@/hooks/use-admin';
import type { components } from '@/api/schema';
import { formatDateTime } from '@/lib/time';
import { cn } from '@/lib/utils';

type ModelServiceItem = components['schemas']['service.ModelServiceItem'];
type ModelEntryItem = components['schemas']['service.ModelEntryItem'];

function toastError(e: unknown): void {
  toast.error(e instanceof Error ? e.message : '操作失败，请稍后重试');
}

const PURPOSE_LABELS: Record<string, string> = {
  chat: '对话',
  embedding: '向量',
  rerank: '重排',
};

function PurposeBadge({ purpose }: { purpose?: string }) {
  const label = PURPOSE_LABELS[purpose ?? ''] ?? purpose ?? '—';
  return (
    <span className="inline-flex items-center rounded-md border border-[var(--border)] bg-[var(--primary-soft)] px-1.5 py-0.5 text-xs text-[var(--primary)]">
      {label}
    </span>
  );
}

function EnabledBadge({ enabled }: { enabled?: boolean }) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
        enabled
          ? 'bg-[var(--accent-soft)] text-[var(--accent)]'
          : 'bg-[var(--muted-bg)] text-[var(--muted-foreground)]',
      )}
    >
      {enabled ? '启用' : '停用'}
    </span>
  );
}

/** 测试连接三用途结果徽标：值 "ok" 绿 / "err: ..." 红带详情。 */
function TestResultBadges({ result }: { result: ModelTestResult }) {
  const entries: [string, string | undefined][] = [
    ['对话', result.chat],
    ['向量', result.embedding],
    ['重排', result.rerank],
  ];
  return (
    <span className="inline-flex items-center gap-1.5" data-testid="test-result">
      {entries
        .filter(([, v]) => v !== undefined)
        .map(([label, v]) => {
          const ok = v === 'ok';
          return (
            <span
              key={label}
              title={v}
              className={cn(
                'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px]',
                ok
                  ? 'border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--accent)]'
                  : 'border-[var(--destructive)] bg-[var(--destructive-soft)] text-[var(--destructive)]',
              )}
            >
              {ok ? (
                <CircleCheck className="size-3" aria-hidden="true" />
              ) : (
                <CircleAlert className="size-3" aria-hidden="true" />
              )}
              {label} {ok ? '正常' : '异常'}
            </span>
          );
        })}
    </span>
  );
}

/** 校验 params JSON 文本（空串=无参数）。 */
function parseParams(text: string): { ok: true; value?: Record<string, unknown> } | { ok: false; error: string } {
  const trimmed = text.trim();
  if (trimmed.length === 0) return { ok: true };
  try {
    const parsed: unknown = JSON.parse(trimmed);
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      return { ok: false, error: '参数须为 JSON 对象（如 {"temperature": 0.2}）' };
    }
    return { ok: true, value: parsed as Record<string, unknown> };
  } catch {
    return { ok: false, error: '参数不是合法 JSON' };
  }
}

export function AdminModelServicesPage() {
  const services = useModelServices();
  const [entryFilters, setEntryFilters] = useState<{ service_id: number | null; purpose: string | null }>({
    service_id: null,
    purpose: null,
  });
  const entries = useModelEntries(entryFilters);

  const createService = useCreateModelService();
  const patchService = usePatchModelService();
  const deleteService = useDeleteModelService();
  const testService = useTestModelService();
  const createEntry = useCreateModelEntry();
  const patchEntry = usePatchModelEntry();
  const deleteEntry = useDeleteModelEntry();

  // 测试连接结果（按服务 ID 暂存）
  const [testResults, setTestResults] = useState<Record<number, ModelTestResult>>({});

  // 服务 dialog（新建/编辑共用）
  const [serviceDialog, setServiceDialog] = useState<
    { mode: 'create' } | { mode: 'edit'; service: ModelServiceItem } | null
  >(null);
  const [svcName, setSvcName] = useState('');
  const [svcBaseUrl, setSvcBaseUrl] = useState('');
  const [svcApiKey, setSvcApiKey] = useState('');

  // 删除服务确认
  const [pendingDeleteService, setPendingDeleteService] = useState<ModelServiceItem | null>(null);
  // 条目 dialog（新建/编辑共用）
  const [entryDialog, setEntryDialog] = useState<
    { mode: 'create'; serviceId?: number } | { mode: 'edit'; entry: ModelEntryItem } | null
  >(null);
  const [entryServiceId, setEntryServiceId] = useState<string>('');
  const [entryPurpose, setEntryPurpose] = useState<string>('chat');
  const [entryModelName, setEntryModelName] = useState('');
  const [entryParams, setEntryParams] = useState('');
  const [entryIsDefault, setEntryIsDefault] = useState(false);
  // 删除条目确认
  const [pendingDeleteEntry, setPendingDeleteEntry] = useState<ModelEntryItem | null>(null);

  const openServiceDialog = (
    mode: 'create' | 'edit',
    service?: ModelServiceItem,
  ) => {
    setSvcName(service?.name ?? '');
    setSvcBaseUrl(service?.base_url ?? '');
    setSvcApiKey('');
    setServiceDialog(mode === 'edit' && service ? { mode, service } : { mode: 'create' });
  };

  const handleSaveService = () => {
    const name = svcName.trim();
    const baseUrl = svcBaseUrl.trim();
    if (name.length === 0 || baseUrl.length === 0) {
      toast.error('服务名称与 Base URL 不能为空');
      return;
    }
    const apiKey = svcApiKey.trim();
    const dialog = serviceDialog;
    if (!dialog) return;
    if (dialog.mode === 'create') {
      createService.mutate(
        { name, base_url: baseUrl, api_key: apiKey || undefined },
        {
          onSuccess: () => {
            toast.success('服务已创建');
            setServiceDialog(null);
          },
          onError: toastError,
        },
      );
    } else {
      patchService.mutate(
        {
          id: dialog.service.id ?? 0,
          body: {
            name,
            base_url: baseUrl,
            // key 字段缺省=不修改（placeholder「留空不修改」）
            ...(apiKey ? { api_key: apiKey } : {}),
          },
        },
        {
          onSuccess: () => {
            toast.success('服务已更新');
            setServiceDialog(null);
          },
          onError: toastError,
        },
      );
    }
  };

  const handleToggleService = (service: ModelServiceItem) => {
    patchService.mutate(
      { id: service.id ?? 0, body: { enabled: !service.enabled } },
      {
        onSuccess: () => toast.success(service.enabled ? '服务已停用' : '服务已启用'),
        onError: toastError,
      },
    );
  };

  const handleTestService = (service: ModelServiceItem) => {
    testService.mutate(service.id ?? 0, {
      onSuccess: (result) => {
        setTestResults((prev) => ({ ...prev, [service.id ?? 0]: result }));
      },
      onError: toastError,
    });
  };

  const openEntryDialog = (mode: 'create' | 'edit', opts?: { serviceId?: number; entry?: ModelEntryItem }) => {
    setEntryServiceId(String(opts?.entry?.service_id ?? opts?.serviceId ?? ''));
    setEntryPurpose(opts?.entry?.purpose ?? 'chat');
    setEntryModelName(opts?.entry?.model_name ?? '');
    setEntryParams(opts?.entry?.params ?? '');
    setEntryIsDefault(opts?.entry?.is_default ?? false);
    setEntryDialog(mode === 'edit' && opts?.entry ? { mode, entry: opts.entry } : { mode: 'create', serviceId: opts?.serviceId });
  };

  const handleSaveEntry = () => {
    const modelName = entryModelName.trim();
    const serviceId = Number(entryServiceId);
    if (!Number.isFinite(serviceId) || serviceId <= 0) {
      toast.error('请选择所属服务');
      return;
    }
    if (modelName.length === 0) {
      toast.error('模型名称不能为空');
      return;
    }
    const params = parseParams(entryParams);
    if (!params.ok) {
      toast.error(params.error);
      return;
    }
    const dialog = entryDialog;
    if (!dialog) return;
    if (dialog.mode === 'create') {
      createEntry.mutate(
        {
          service_id: serviceId,
          purpose: entryPurpose,
          model_name: modelName,
          ...(params.value ? { params: params.value } : {}),
          is_default: entryIsDefault || undefined,
        },
        {
          onSuccess: () => {
            toast.success('条目已创建');
            setEntryDialog(null);
          },
          onError: toastError,
        },
      );
    } else {
      patchEntry.mutate(
        {
          id: dialog.entry.id ?? 0,
          body: {
            purpose: entryPurpose,
            model_name: modelName,
            ...(params.value ? { params: params.value } : {}),
            ...(entryIsDefault ? { is_default: true } : {}),
          },
        },
        {
          onSuccess: () => {
            toast.success('条目已更新');
            setEntryDialog(null);
          },
          onError: toastError,
        },
      );
    }
  };

  const handleSetDefault = (entry: ModelEntryItem) => {
    patchEntry.mutate(
      { id: entry.id ?? 0, body: { is_default: true } },
      {
        onSuccess: () => toast.success('已设为该用途默认'),
        onError: toastError,
      },
    );
  };

  const servicesList = services.data ?? [];

  return (
    <div className="mx-auto w-full max-w-5xl space-y-8">
      {/* 服务列表 */}
      <section>
        <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-lg font-semibold">模型服务</h1>
            <p className="text-sm text-[var(--muted)]">
              OpenAI 兼容端点接入；API Key 加密落库、永不回显（仅 has_key 标记）。
            </p>
          </div>
          <Button onClick={() => openServiceDialog('create')} data-testid="create-service">
            <Plus aria-hidden="true" />
            新建服务
          </Button>
        </div>

        <QueryBoundary
          query={services}
          skeleton={
            <div className="space-y-2">
              {Array.from({ length: 3 }, (_, i) => (
                <Skeleton key={i} className="h-10 w-full" />
              ))}
            </div>
          }
          empty={
            <EmptyState
              icon={Wrench}
              title="还没有模型服务"
              description="先接入一个 OpenAI 兼容服务（对话/向量/重排），再为各用途配置默认模型条目。"
              action={{ label: '新建服务', onClick: () => openServiceDialog('create') }}
            />
          }
        >
          {() => (
            <div className="kb-card p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>名称</TableHead>
                    <TableHead className="w-56">Base URL</TableHead>
                    <TableHead className="w-20">状态</TableHead>
                    <TableHead className="w-16">Key</TableHead>
                    <TableHead className="w-16 text-right">条目</TableHead>
                    <TableHead className="w-36">更新时间</TableHead>
                    <TableHead className="w-64">测试连接</TableHead>
                    <TableHead className="w-52 text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {servicesList.map((svc) => {
                    const result = testResults[svc.id ?? -1];
                    return (
                      <TableRow key={svc.id} data-testid="service-row">
                        <TableCell className="font-medium">{svc.name}</TableCell>
                        <TableCell className="max-w-56 truncate font-mono text-xs" title={svc.base_url}>
                          {svc.base_url}
                        </TableCell>
                        <TableCell>
                          <EnabledBadge enabled={svc.enabled} />
                        </TableCell>
                        <TableCell>
                          {svc.has_key ? (
                            <KeyRound
                              className="size-4 text-[var(--accent)]"
                              aria-label="已配置 API Key"
                            />
                          ) : (
                            <span className="text-xs text-[var(--faint)]">—</span>
                          )}
                        </TableCell>
                        <TableCell className="text-right tabular-nums">{svc.entry_count ?? 0}</TableCell>
                        <TableCell className="text-xs text-[var(--muted)]">
                          {formatDateTime(svc.updated_at) || '—'}
                        </TableCell>
                        <TableCell>
                          {result ? (
                            <TestResultBadges result={result} />
                          ) : (
                            <Button
                              variant="outline"
                              size="sm"
                              disabled={testService.isPending}
                              onClick={() => handleTestService(svc)}
                              data-testid="test-service"
                            >
                              {testService.isPending && testService.variables === svc.id ? (
                                <LoaderCircle className="animate-spin" aria-hidden="true" />
                              ) : null}
                              测试连接
                            </Button>
                          )}
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="flex justify-end gap-1">
                            <Button variant="ghost" size="sm" onClick={() => openServiceDialog('edit', svc)}>
                              编辑
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={patchService.isPending}
                              onClick={() => handleToggleService(svc)}
                              title={svc.enabled ? '停用（MODEL_004：持有默认条目时不可停用）' : '启用'}
                            >
                              {svc.enabled ? '停用' : '启用'}
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              className="text-[var(--destructive)]"
                              onClick={() => setPendingDeleteService(svc)}
                              title="删除（MODEL_003：服务下仍有条目时不可删除）"
                            >
                              删除
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </QueryBoundary>
      </section>

      {/* 条目管理子区 */}
      <section>
        <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-base font-semibold">模型条目</h2>
            <p className="text-sm text-[var(--muted)]">
              按用途（对话/向量/重排）配置模型与参数；每用途仅一条默认条目。
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Select
              value={entryFilters.service_id === null ? 'all' : String(entryFilters.service_id)}
              onValueChange={(v) =>
                setEntryFilters((f) => ({ ...f, service_id: v === 'all' ? null : Number(v) }))
              }
            >
              <SelectTrigger className="w-40" aria-label="按服务筛选">
                <SelectValue placeholder="全部服务" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部服务</SelectItem>
                {servicesList.map((svc) => (
                  <SelectItem key={svc.id} value={String(svc.id)}>
                    {svc.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={entryFilters.purpose ?? 'all'}
              onValueChange={(v) =>
                setEntryFilters((f) => ({ ...f, purpose: v === 'all' ? null : v }))
              }
            >
              <SelectTrigger className="w-32" aria-label="按用途筛选">
                <SelectValue placeholder="全部用途" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部用途</SelectItem>
                <SelectItem value="chat">对话</SelectItem>
                <SelectItem value="embedding">向量</SelectItem>
                <SelectItem value="rerank">重排</SelectItem>
              </SelectContent>
            </Select>
            <Button onClick={() => openEntryDialog('create')} data-testid="create-entry">
              <Plus aria-hidden="true" />
              新建条目
            </Button>
          </div>
        </div>

        <QueryBoundary
          query={entries}
          skeleton={
            <div className="space-y-2">
              {Array.from({ length: 3 }, (_, i) => (
                <Skeleton key={i} className="h-10 w-full" />
              ))}
            </div>
          }
          empty={
            <EmptyState
              icon={Wrench}
              title="没有模型条目"
              description="为服务添加模型条目并设置各用途默认（无默认条目时问答将返回 MODEL_001）。"
              action={{ label: '新建条目', onClick: () => openEntryDialog('create') }}
            />
          }
        >
          {(items) => (
            <div className="kb-card p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-16">用途</TableHead>
                    <TableHead>模型名称</TableHead>
                    <TableHead className="w-32">所属服务</TableHead>
                    <TableHead className="w-56">参数</TableHead>
                    <TableHead className="w-20">默认</TableHead>
                    <TableHead className="w-36">更新时间</TableHead>
                    <TableHead className="w-44 text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((entry) => (
                    <TableRow key={entry.id} data-testid="entry-row">
                      <TableCell>
                        <PurposeBadge purpose={entry.purpose} />
                      </TableCell>
                      <TableCell className="max-w-44 truncate font-mono text-xs" title={entry.model_name}>{entry.model_name}</TableCell>
                      <TableCell className="text-xs">{entry.service_name}</TableCell>
                      <TableCell className="max-w-56 truncate font-mono text-xs text-[var(--muted)]" title={entry.params}>
                        {entry.params || '—'}
                      </TableCell>
                      <TableCell>
                        {entry.is_default ? (
                          <span className="badge flag">默认</span>
                        ) : (
                          <span className="text-xs text-[var(--faint)]">—</span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs text-[var(--muted)]">
                        {formatDateTime(entry.updated_at) || '—'}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1">
                          {!entry.is_default ? (
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={patchEntry.isPending}
                              onClick={() => handleSetDefault(entry)}
                              title="设为该用途默认"
                            >
                              设默认
                            </Button>
                          ) : null}
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => openEntryDialog('edit', { entry })}
                          >
                            编辑
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-[var(--destructive)]"
                            onClick={() => setPendingDeleteEntry(entry)}
                            title="删除（MODEL_002：默认条目不可删除）"
                          >
                            删除
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </QueryBoundary>
      </section>

      {/* 新建/编辑服务 dialog */}
      <Dialog
        open={serviceDialog !== null}
        onOpenChange={(open) => {
          if (!open) setServiceDialog(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {serviceDialog?.mode === 'edit' ? '编辑模型服务' : '新建模型服务'}
            </DialogTitle>
            <DialogDescription>
              Base URL 为 OpenAI 兼容端点前缀（如 https://api.example.com/v1）。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="svc-name">服务名称</Label>
              <Input id="svc-name" value={svcName} onChange={(e) => setSvcName(e.target.value)} placeholder="如：内网 vLLM" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="svc-url">Base URL</Label>
              <Input id="svc-url" value={svcBaseUrl} onChange={(e) => setSvcBaseUrl(e.target.value)} placeholder="https://api.example.com/v1" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="svc-key">API Key</Label>
              <Input
                id="svc-key"
                type="password"
                value={svcApiKey}
                onChange={(e) => setSvcApiKey(e.target.value)}
                placeholder={serviceDialog?.mode === 'edit' ? '留空不修改' : '服务密钥（可留空）'}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setServiceDialog(null)} disabled={createService.isPending || patchService.isPending}>
              取消
            </Button>
            <Button onClick={handleSaveService} disabled={createService.isPending || patchService.isPending}>
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 删除服务确认（MODEL_003 由后端拦截 → toast） */}
      <ConfirmDialog
        open={pendingDeleteService !== null}
        onOpenChange={(open) => {
          if (!open) setPendingDeleteService(null);
        }}
        title="删除模型服务"
        description={`将删除服务「${pendingDeleteService?.name ?? ''}」。服务下仍有模型条目时将被拒绝（MODEL_003），请先删除全部条目。`}
        confirmLabel="删除"
        busy={deleteService.isPending}
        onConfirm={() => {
          if (!pendingDeleteService?.id) return;
          deleteService.mutate(pendingDeleteService.id, {
            onSuccess: () => {
              toast.success('服务已删除');
              setPendingDeleteService(null);
            },
            onError: toastError,
          });
        }}
      />

      {/* 新建/编辑条目 dialog */}
      <Dialog
        open={entryDialog !== null}
        onOpenChange={(open) => {
          if (!open) setEntryDialog(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {entryDialog?.mode === 'edit' ? '编辑模型条目' : '新建模型条目'}
            </DialogTitle>
            <DialogDescription>
              用途决定调用场景；勾选默认将成为该用途唯一默认条目（原默认自动让位）。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="entry-service">所属服务</Label>
              <Select value={entryServiceId} onValueChange={setEntryServiceId}>
                <SelectTrigger className="w-full" id="entry-service" aria-label="所属服务">
                  <SelectValue placeholder="选择服务" />
                </SelectTrigger>
                <SelectContent>
                  {servicesList.map((svc) => (
                    <SelectItem key={svc.id} value={String(svc.id)}>
                      {svc.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="entry-purpose">用途</Label>
              <Select value={entryPurpose} onValueChange={setEntryPurpose}>
                <SelectTrigger className="w-full" id="entry-purpose" aria-label="用途">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="chat">对话（chat）</SelectItem>
                  <SelectItem value="embedding">向量（embedding）</SelectItem>
                  <SelectItem value="rerank">重排（rerank）</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="entry-model">模型名称</Label>
              <Input
                id="entry-model"
                value={entryModelName}
                onChange={(e) => setEntryModelName(e.target.value)}
                placeholder="如：gpt-4o-mini / text-embedding-3-small"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="entry-params">模型参数（JSON，选填）</Label>
              <Textarea
                id="entry-params"
                value={entryParams}
                onChange={(e) => setEntryParams(e.target.value)}
                placeholder='如 {"temperature": 0.2, "top_n": 5}'
                rows={3}
                className="resize-none font-mono text-xs"
              />
            </div>
            <label className="flex cursor-pointer items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={entryIsDefault}
                onChange={(e) => setEntryIsDefault(e.target.checked)}
                className="accent-[var(--primary)]"
              />
              设为该用途默认
            </label>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEntryDialog(null)} disabled={createEntry.isPending || patchEntry.isPending}>
              取消
            </Button>
            <Button onClick={handleSaveEntry} disabled={createEntry.isPending || patchEntry.isPending}>
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 删除条目确认（MODEL_002 由后端拦截 → toast） */}
      <ConfirmDialog
        open={pendingDeleteEntry !== null}
        onOpenChange={(open) => {
          if (!open) setPendingDeleteEntry(null);
        }}
        title="删除模型条目"
        description={`将删除条目「${pendingDeleteEntry?.model_name ?? ''}」（${PURPOSE_LABELS[pendingDeleteEntry?.purpose ?? ''] ?? ''}）。默认条目不可删除（MODEL_002），请先切换默认。`}
        confirmLabel="删除"
        busy={deleteEntry.isPending}
        onConfirm={() => {
          if (!pendingDeleteEntry?.id) return;
          deleteEntry.mutate(pendingDeleteEntry.id, {
            onSuccess: () => {
              toast.success('条目已删除');
              setPendingDeleteEntry(null);
            },
            onError: toastError,
          });
        }}
      />
    </div>
  );
}
