/**
 * IngestWizard：接入向导（Git / 上传 / 网页 三种知识源，dialog 内 Tab 切换）。
 * - Git：repo_url 必填 + 凭证可选（kind=token secret / kind=ssh PEM textarea）+ ignore_rules
 *   （逗号/换行分隔 glob，可选）；DOC_001/003 错误展示
 * - 上传：拖拽区 + 多选；前端预校验 ≤50 个、单文件 ≤5MB、扩展名白名单（R1/R2）；
 *   DOC_002 逐个报错展示（服务端 rejected + 前端预校验合并展示）
 * - URL：url 必填
 * 提交成功 → 父组件 invalidate 文档列表（新文档进入轮询）。
 * 按钮可见性由调用方按 R6 控制（编辑者+）。
 */

import { useRef, useState, type ChangeEvent, type DragEvent, type ReactNode } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { UploadCloud } from 'lucide-react';
import { cn } from '@/lib/utils';
import {
  MAX_UPLOAD_FILES,
  parseIgnoreRules,
  validateUploadFiles,
  type UploadFileError,
} from '@/lib/ingest';
import { useGitIngest, useUploadFiles, useUrlIngest } from '@/hooks/use-documents';

export type IngestMode = 'git' | 'upload' | 'url';

interface IngestWizardProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  kbId: number;
  /** 默认打开的接入方式（从 EmptyState CTA 进入时指定） */
  initialMode?: IngestMode;
}

/** Tab 通用样式（复用 nav-item 视觉）。 */
function ModeTab({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      className={cn('nav-item', active && 'active')}
      onClick={onClick}
      aria-pressed={active}
    >
      {children}
    </button>
  );
}

/** 表单级错误条（ApiError 文案 + 逐字段红字由 RHF 处理）。 */
function FormError({ message }: { message: string | null }) {
  if (!message) return null;
  return (
    <p className="rounded-[var(--radius)] bg-[var(--destructive-soft)] px-3 py-2 text-xs text-[var(--destructive)]">
      {message}
    </p>
  );
}

/* ------------------------------ Git 接入 ------------------------------ */

const gitSchema = z.object({
  repo_url: z.string().min(1, '请输入仓库地址'),
  git_ref: z.string().optional(),
  ignore_rules: z.string().optional(),
});

type GitFormValues = z.infer<typeof gitSchema>;

type GitCredentialKind = 'none' | 'token' | 'ssh';

function GitIngestForm({ kbId, onSuccess }: { kbId: number; onSuccess: () => void }) {
  const [kind, setKind] = useState<GitCredentialKind>('none');
  const [secret, setSecret] = useState('');
  const [error, setError] = useState<string | null>(null);
  const gitIngest = useGitIngest(kbId);

  const form = useForm<GitFormValues>({
    resolver: zodResolver(gitSchema),
    defaultValues: { repo_url: '', git_ref: '', ignore_rules: '' },
  });

  const onSubmit = form.handleSubmit(async (values) => {
    setError(null);
    const credential =
      kind === 'none' || secret.trim() === '' ? undefined : { kind, secret: secret.trim() };
    try {
      await gitIngest.mutateAsync({
        repo_url: values.repo_url.trim(),
        git_ref: values.git_ref?.trim() || undefined,
        credential,
        ignore_rules: parseIgnoreRules(values.ignore_rules ?? ''),
      });
      onSuccess();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Git 接入失败，请稍后重试');
    }
  });

  const submitting = form.formState.isSubmitting || gitIngest.isPending;

  return (
    <form className="space-y-4" onSubmit={onSubmit}>
      <div className="space-y-1.5">
        <Label htmlFor="git-repo-url">仓库地址 *</Label>
        <Input
          id="git-repo-url"
          placeholder="https://…/repo.git 或 git@…:team/repo.git"
          {...form.register('repo_url')}
        />
        {form.formState.errors.repo_url ? (
          <p className="text-xs text-[var(--destructive)]">
            {form.formState.errors.repo_url.message}
          </p>
        ) : null}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="git-ref">分支 / 标签（可选，空 = 默认分支）</Label>
        <Input id="git-ref" placeholder="main" {...form.register('git_ref')} />
      </div>

      <div className="space-y-1.5">
        <Label>凭证（私有仓库必填，可选）</Label>
        <div className="flex gap-2">
          {(
            [
              ['none', '不使用'],
              ['token', 'Token'],
              ['ssh', 'SSH 私钥'],
            ] as const
          ).map(([value, label]) => (
            <ModeTab key={value} active={kind === value} onClick={() => setKind(value)}>
              {label}
            </ModeTab>
          ))}
        </div>
        {kind === 'token' ? (
          <Input
            type="password"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            placeholder="访问令牌（HTTPS BasicAuth 密码位）"
            autoComplete="off"
          />
        ) : null}
        {kind === 'ssh' ? (
          <Textarea
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            rows={5}
            placeholder={'-----BEGIN OPENSSH PRIVATE KEY-----\n…\n-----END OPENSSH PRIVATE KEY-----'}
            className="font-mono text-xs"
            autoComplete="off"
          />
        ) : null}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="git-ignore-rules">忽略规则（可选，逗号或换行分隔 glob）</Label>
        <Textarea
          id="git-ignore-rules"
          rows={3}
          placeholder={'docs/drafts/**\n*.pem, secrets/'}
          {...form.register('ignore_rules')}
        />
      </div>

      <FormError message={error} />

      <div className="flex justify-end gap-2">
        <Button type="submit" disabled={submitting}>
          {submitting ? '接入中…' : '接入仓库'}
        </Button>
      </div>
    </form>
  );
}

/* ------------------------------ 文件上传 ------------------------------ */

function UploadIngestForm({ kbId, onSuccess }: { kbId: number; onSuccess: () => void }) {
  const [files, setFiles] = useState<File[]>([]);
  const [preErrors, setPreErrors] = useState<UploadFileError[]>([]);
  const [serverErrors, setServerErrors] = useState<UploadRejectedItem[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [dragOver, setDragOver] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const upload = useUploadFiles(kbId);

  const setFromList = (list: FileList | null) => {
    setError(null);
    setServerErrors([]);
    const { accepted, errors } = validateUploadFiles(list ? Array.from(list) : []);
    setFiles(accepted);
    setPreErrors(errors);
  };

  const onDrop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setDragOver(false);
    setFromList(e.dataTransfer.files);
  };

  const onChange = (e: ChangeEvent<HTMLInputElement>) => {
    setFromList(e.target.files);
  };

  const submit = async () => {
    setError(null);
    setServerErrors([]);
    if (files.length === 0) {
      setError('请先选择要上传的文件');
      return;
    }
    try {
      const result = await upload.mutateAsync(files);
      const rejected = result.rejected ?? [];
      if (rejected.length > 0) {
        // 部分文件被服务端拒绝（DOC_002）：文档列表已由 mutation onSuccess invalidate，
        // 成功入库的文件已进入轮询；不关窗，让用户看到逐个报错
        setServerErrors(rejected);
        return;
      }
      onSuccess();
    } catch (e) {
      setError(e instanceof Error ? e.message : '上传失败，请稍后重试');
    }
  };

  const submitting = upload.isPending;

  return (
    <div className="space-y-4">
      <div
        role="button"
        tabIndex={0}
        aria-label="拖拽或点击选择文件"
        className={cn(
          'flex flex-col items-center gap-2 rounded-[var(--radius)] border-2 border-dashed px-6 py-10 text-center transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--primary)]',
          dragOver
            ? 'border-[var(--border-strong)] bg-[var(--primary-soft)]'
            : 'border-[var(--border)] hover:bg-[var(--primary-soft)]',
        )}
        onDragOver={(e) => {
          e.preventDefault();
          setDragOver(true);
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={onDrop}
        onClick={() => inputRef.current?.click()}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') inputRef.current?.click();
        }}
      >
        <UploadCloud className="size-8 text-[var(--faint)]" aria-hidden="true" />
        <p className="text-sm">拖拽文件到此处，或点击选择</p>
        <p className="text-xs text-[var(--faint)]">
          单次最多 {MAX_UPLOAD_FILES} 个 · 单文件 ≤5MB · Markdown / 文本 / 常见代码源文件
        </p>
        <input
          ref={inputRef}
          type="file"
          multiple
          className="hidden"
          onChange={onChange}
          aria-hidden="true"
        />
      </div>

      {files.length > 0 ? (
        <p className="text-sm text-[var(--muted)]">已选择 {files.length} 个文件，将逐个创建文档</p>
      ) : null}

      {preErrors.length > 0 ? (
        <div className="space-y-1 rounded-[var(--radius)] bg-[var(--destructive-soft)] px-3 py-2">
          <p className="text-xs font-medium text-[var(--destructive)]">以下文件被拒绝：</p>
          {preErrors.map((err) => (
            <p key={err.name} className="text-xs text-[var(--destructive)]">
              {err.name}：{err.reason}
            </p>
          ))}
        </div>
      ) : null}

      {serverErrors.length > 0 ? (
        <div className="space-y-1 rounded-[var(--radius)] bg-[var(--destructive-soft)] px-3 py-2">
          <p className="text-xs font-medium text-[var(--destructive)]">服务端拒绝以下文件：</p>
          {serverErrors.map((r, i) => (
            <p key={`${r.code}-${i}`} className="text-xs text-[var(--destructive)]">
              {r.message}
            </p>
          ))}
        </div>
      ) : null}

      <FormError message={error} />

      <div className="flex justify-end gap-2">
        <Button onClick={submit} disabled={submitting || files.length === 0}>
          {submitting ? '上传中…' : `上传 ${files.length} 个文件`}
        </Button>
      </div>
    </div>
  );
}

/** 上传 rejected 条目（与 manual-body.ts UploadRejected 一致，避免跨 import 循环） */
interface UploadRejectedItem {
  code: string;
  message: string;
}

/* ------------------------------ 网页接入 ------------------------------ */

const urlSchema = z.object({
  url: z.string().min(1, '请输入网页地址').url('请输入合法的 URL（含协议）'),
});

type UrlFormValues = z.infer<typeof urlSchema>;

function UrlIngestForm({ kbId, onSuccess }: { kbId: number; onSuccess: () => void }) {
  const [error, setError] = useState<string | null>(null);
  const urlIngest = useUrlIngest(kbId);

  const form = useForm<UrlFormValues>({
    resolver: zodResolver(urlSchema),
    defaultValues: { url: '' },
  });

  const onSubmit = form.handleSubmit(async (values) => {
    setError(null);
    try {
      await urlIngest.mutateAsync(values.url.trim());
      onSuccess();
    } catch (e) {
      setError(e instanceof Error ? e.message : '网页接入失败，请稍后重试');
    }
  });

  const submitting = form.formState.isSubmitting || urlIngest.isPending;

  return (
    <form className="space-y-4" onSubmit={onSubmit}>
      <div className="space-y-1.5">
        <Label htmlFor="ingest-url">网页地址 *</Label>
        <Input
          id="ingest-url"
          placeholder="https://example.com/docs/page"
          {...form.register('url')}
        />
        {form.formState.errors.url ? (
          <p className="text-xs text-[var(--destructive)]">{form.formState.errors.url.message}</p>
        ) : null}
      </div>

      <FormError message={error} />

      <div className="flex justify-end gap-2">
        <Button type="submit" disabled={submitting}>
          {submitting ? '接入中…' : '接入网页'}
        </Button>
      </div>
    </form>
  );
}

/* ------------------------------ 向导容器 ------------------------------ */

const MODE_TITLES: Record<IngestMode, string> = {
  git: '接入 Git 仓库',
  upload: '上传文件',
  url: '接入网页',
};

const MODE_LABELS: Record<IngestMode, string> = {
  git: 'Git 仓库',
  upload: '上传文件',
  url: '网页',
};

export function IngestWizard({ open, onOpenChange, kbId, initialMode = 'git' }: IngestWizardProps) {
  const [mode, setMode] = useState<IngestMode>(initialMode);

  const closeAndReset = () => {
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>接入知识源</DialogTitle>
          <DialogDescription>
            支持三种知识源：Git 仓库、本地文件上传、网页抓取。接入后文档进入异步摄取，状态就绪即可检索与问答。
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-wrap gap-1.5 pb-1">
          {(Object.keys(MODE_TITLES) as IngestMode[]).map((m) => (
            <ModeTab key={m} active={mode === m} onClick={() => setMode(m)}>
              {MODE_LABELS[m]}
            </ModeTab>
          ))}
        </div>

        {mode === 'git' ? <GitIngestForm kbId={kbId} onSuccess={closeAndReset} /> : null}
        {mode === 'upload' ? <UploadIngestForm kbId={kbId} onSuccess={closeAndReset} /> : null}
        {mode === 'url' ? <UrlIngestForm kbId={kbId} onSuccess={closeAndReset} /> : null}
      </DialogContent>
    </Dialog>
  );
}
