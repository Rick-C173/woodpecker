/**
 * 强制改密页（F3）：新密码 + 确认密码，zod ≥8 位（AUTH_006 前端拦截）。
 * 首登场景下 old_password = 管理员下发的一次性初始密码，用户需手动填入。
 * 成功后 token_version+1（响应中的新 token 不采用）→ 提示并清认证跳登录重新认证。
 */

import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { toast } from 'sonner';
import { ThemeControls } from '@/components/theme/theme-controls';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { clearAuth } from '@/lib/auth';
import { postChangePassword } from '@/api/manual-body';

const changePasswordSchema = z
  .object({
    old_password: z.string().min(1, '请输入当前密码'),
    new_password: z.string().min(8, '密码长度至少 8 位'),
    confirm_password: z.string().min(1, '请再次输入新密码'),
  })
  .refine((v) => v.new_password === v.confirm_password, {
    path: ['confirm_password'],
    message: '两次输入的新密码不一致',
  });

type ChangePasswordFormValues = z.infer<typeof changePasswordSchema>;

export function ChangePasswordPage() {
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);

  const form = useForm<ChangePasswordFormValues>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: { old_password: '', new_password: '', confirm_password: '' },
  });

  const onSubmit = form.handleSubmit(async (values) => {
    setError(null);
    try {
      const { response } = await postChangePassword({
        old_password: values.old_password,
        new_password: values.new_password,
      });
      if (!response.ok) {
        setError('修改失败，请稍后重试');
        return;
      }
      // token_version+1：响应携带的新 token 不采用，重新登录取得干净的会话
      clearAuth();
      toast.success('密码修改成功，请使用新密码重新登录');
      navigate('/login', { replace: true });
    } catch (e) {
      // AUTH_001 原密码错误 / AUTH_006 新密码过短 → errors.ts 文案
      setError(e instanceof Error ? e.message : '修改失败，请稍后重试');
    }
  });

  const submitting = form.formState.isSubmitting;
  const errors = form.formState.errors;

  return (
    <div className="flex h-screen items-center justify-center">
      <div className="login-card">
        <div className="mb-4">
          <ThemeControls />
        </div>
        <div className="login-logo" aria-hidden="true">
          🔐
        </div>
        <h1 className="text-lg">修改初始密码</h1>
        <p className="mb-5 mt-1 text-[13px] text-[var(--muted)]">
          首次登录或初始密码未修改时，需要先设置新密码（至少 8 位）才能继续使用。
        </p>

        <form className="space-y-4" onSubmit={onSubmit} noValidate>
          <div className="space-y-1.5">
            <Label htmlFor="cp-old">当前密码</Label>
            <Input
              id="cp-old"
              type="password"
              autoComplete="current-password"
              placeholder="管理员下发的一次性初始密码"
              {...form.register('old_password')}
            />
            {errors.old_password ? (
              <p className="text-xs text-[var(--destructive)]">{errors.old_password.message}</p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="cp-new">新密码（至少 8 位）</Label>
            <Input
              id="cp-new"
              type="password"
              autoComplete="new-password"
              {...form.register('new_password')}
            />
            {errors.new_password ? (
              <p className="text-xs text-[var(--destructive)]">{errors.new_password.message}</p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="cp-confirm">确认新密码</Label>
            <Input
              id="cp-confirm"
              type="password"
              autoComplete="new-password"
              {...form.register('confirm_password')}
            />
            {errors.confirm_password ? (
              <p className="text-xs text-[var(--destructive)]">{errors.confirm_password.message}</p>
            ) : null}
          </div>

          {error ? (
            <p className="rounded-[var(--radius)] bg-[var(--destructive-soft)] px-3 py-2 text-xs text-[var(--destructive)]">
              {error}
            </p>
          ) : null}

          <Button type="submit" className="w-full" disabled={submitting}>
            {submitting ? '提交中…' : '确认修改'}
          </Button>
        </form>
      </div>
    </div>
  );
}
