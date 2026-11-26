/**
 * 持邀注册页（FR-16，公开路由 /register?invite=<token>）：
 * 加载时校验邀请（邮箱只读展示）→ 用户名/密码表单 → /auth/register/invited。
 * 无效/过期/已用分别给出明确提示并引导联系管理员。
 */

import { useEffect, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { ThemeToggleButton } from '@/components/theme/theme-controls';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { setAuth, type AuthUser } from '@/lib/auth';
import { getInvitation, postRegisterInvited } from '@/api/manual-body';

const registerSchema = z.object({
  username: z.string().min(1, '请输入用户名'),
  password: z.string().min(8, '密码至少 8 位'),
});

type RegisterFormValues = z.infer<typeof registerSchema>;

type LoadState =
  | { kind: 'loading' }
  | { kind: 'valid'; email: string; expires: string }
  | { kind: 'invalid'; message: string };

export function RegisterPage() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const token = searchParams.get('invite') ?? '';
  const [load, setLoad] = useState<LoadState>({ kind: 'loading' });
  const [error, setError] = useState<string | null>(null);

  const form = useForm<RegisterFormValues>({
    resolver: zodResolver(registerSchema),
    defaultValues: { username: '', password: '' },
  });

  useEffect(() => {
    if (!token) {
      setLoad({ kind: 'invalid', message: '缺少邀请参数，请通过邮件中的链接访问。' });
      return;
    }
    void (async () => {
      try {
        const { data, response } = await getInvitation(token);
        if (!response.ok) {
          setLoad({ kind: 'invalid', message: '邀请无效或已过期，请联系管理员重新邀请。' });
          return;
        }
        setLoad({ kind: 'valid', email: data?.email ?? '', expires: data?.expires_at ?? '' });
      } catch {
        setLoad({ kind: 'invalid', message: '邀请校验失败，请稍后重试或联系管理员。' });
      }
    })();
  }, [token]);

  const onSubmit = form.handleSubmit(async (values) => {
    setError(null);
    try {
      const { data, response } = await postRegisterInvited({
        token,
        username: values.username,
        password: values.password,
      });
      if (!response.ok || !data?.token) {
        setError('注册失败，请稍后重试');
        return;
      }
      const user: AuthUser = {
        user_id: data.user_id ?? 0,
        username: data.username ?? '',
        is_admin: data.is_admin ?? false,
        must_change_password: data.must_change_password ?? false,
      };
      setAuth(data.token, user);
      navigate('/kbs', { replace: true });
    } catch (e) {
      setError(e instanceof Error ? e.message : '注册失败，请稍后重试');
    }
  });

  return (
    <div className="relative flex min-h-svh items-center justify-center px-4">
      <div className="absolute right-4 top-4">
        <ThemeToggleButton />
      </div>
      <div className="w-full max-w-sm rounded-xl border border-[var(--border)] bg-[var(--card)] p-6 shadow-sm">
        <h1 className="text-lg font-semibold">完成注册</h1>

        {load.kind === 'loading' && (
          <p className="mt-4 text-sm text-[var(--faint)]">正在校验邀请…</p>
        )}

        {load.kind === 'invalid' && (
          <div className="mt-4 space-y-3">
            <p className="text-sm text-[var(--destructive)]">{load.message}</p>
            <Link to="/login" className="text-sm underline">
              返回登录
            </Link>
          </div>
        )}

        {load.kind === 'valid' && (
          <form className="mt-4 space-y-4" onSubmit={onSubmit}>
            <div className="grid gap-2">
              <Label htmlFor="reg-email">受邀邮箱</Label>
              <Input id="reg-email" value={load.email} readOnly disabled />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="reg-username">用户名</Label>
              <Input id="reg-username" {...form.register('username')} placeholder="登录用户名" />
              {form.formState.errors.username && (
                <p className="text-xs text-[var(--destructive)]">
                  {form.formState.errors.username.message}
                </p>
              )}
            </div>
            <div className="grid gap-2">
              <Label htmlFor="reg-password">密码（至少 8 位）</Label>
              <Input
                id="reg-password"
                type="password"
                {...form.register('password')}
                placeholder="自设登录密码"
              />
              {form.formState.errors.password && (
                <p className="text-xs text-[var(--destructive)]">
                  {form.formState.errors.password.message}
                </p>
              )}
            </div>
            {error && <p className="text-sm text-[var(--destructive)]">{error}</p>}
            <Button type="submit" className="w-full" disabled={form.formState.isSubmitting}>
              {form.formState.isSubmitting ? '注册中…' : '完成注册'}
            </Button>
            <p className="text-xs text-[var(--faint)]">
              注册后即可登录知识库。遇到问题请联系管理员重新发送邀请。
            </p>
          </form>
        )}
      </div>
    </div>
  );
}
