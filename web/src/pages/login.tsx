/**
 * 登录页（F3）：RHF + zod 表单 + 真实 /auth/login 接口。
 * AUTH_001/002/003 错误文案走 errors.ts 映射表（登录页是公开端点，401 不会误触全局登出）；
 * 成功后按 must_change_password 分流（→ /change-password），否则回 from 或 /kbs。
 * 卡片顶部保留主题/配色切换入口（架构 §5.4 第 7 条）。
 */

import { useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { setAuth, type AuthUser } from '@/lib/auth';
import { postLogin } from '@/api/manual-body';

const loginSchema = z.object({
  username: z.string().min(1, '请输入用户名'),
  password: z.string().min(1, '请输入密码'),
});

type LoginFormValues = z.infer<typeof loginSchema>;

export function LoginPage() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [error, setError] = useState<string | null>(null);

  const form = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { username: '', password: '' },
  });

  const onSubmit = form.handleSubmit(async (values) => {
    setError(null);
    try {
      const { data, response } = await postLogin(values);
      if (!response.ok || !data?.token) {
        setError('登录失败，请稍后重试');
        return;
      }
      const user: AuthUser = {
        user_id: data.user_id ?? 0,
        username: data.username ?? '',
        is_admin: data.is_admin ?? false,
        must_change_password: data.must_change_password ?? false,
      };
      setAuth(data.token, user);

      // 首登强制改密 → 改密页；否则回 from（防开放重定向：只接受站内路径）
      const from = searchParams.get('from');
      const target =
        user.must_change_password
          ? '/change-password'
          : from && from.startsWith('/') && !from.startsWith('//')
            ? from
            : '/kbs';
      navigate(target, { replace: true });
    } catch (e) {
      // AUTH_001/002/003 等已知码走 errors.ts 文案；未知兜底后端 message
      setError(e instanceof Error ? e.message : '登录失败，请稍后重试');
    }
  });

  const submitting = form.formState.isSubmitting;

  return (
    <div className="flex h-screen items-center justify-center">
      <div className="login-card">
        <div className="login-logo" aria-hidden="true">
          🐦
        </div>
        <h1 className="text-xl">啄木鸟 Woodpecker</h1>
        <p className="mb-6 mt-1 text-[13px] text-[var(--muted)]">企业内部知识库问答系统</p>

        <form className="space-y-4" onSubmit={onSubmit} noValidate>
          <div className="space-y-1.5">
            <Label htmlFor="login-username">用户名或邮箱</Label>
            <Input
              id="login-username"
              autoComplete="username"
              placeholder="用户名或邮箱"
              {...form.register('username')}
            />
            {form.formState.errors.username ? (
              <p className="text-xs text-[var(--destructive)]">
                {form.formState.errors.username.message}
              </p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="login-password">密码</Label>
            <Input
              id="login-password"
              type="password"
              autoComplete="current-password"
              placeholder="密码"
              {...form.register('password')}
            />
            {form.formState.errors.password ? (
              <p className="text-xs text-[var(--destructive)]">
                {form.formState.errors.password.message}
              </p>
            ) : null}
          </div>

          {error ? (
            <p className="rounded-[var(--radius)] bg-[var(--destructive-soft)] px-3 py-2 text-xs text-[var(--destructive)]">
              {error}
            </p>
          ) : null}

          <Button type="submit" className="w-full" disabled={submitting}>
            {submitting ? '登录中…' : '登录'}
          </Button>
        </form>

        <p className="mt-4 text-center text-xs text-[var(--faint)]">
          账号由管理员统一创建 · 忘记密码请联系管理员
        </p>
      </div>
    </div>
  );
}
