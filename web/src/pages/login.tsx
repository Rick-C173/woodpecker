/**
 * 登录页（F3）：RHF + zod 表单 + 真实 /auth/login 接口。
 * AUTH_001/002/003 错误文案走 errors.ts 映射表（登录页是公开端点，401 不会误触全局登出）；
 * 成功后按 must_change_password 分流（→ /change-password），否则回 from 或 /kbs。
 * 卡片顶部保留主题/配色切换入口（架构 §5.4 第 7 条）。
 * M13（FR-19）：SSO 回跳处理——`?oidc=<一次性code>` 兑换 JWT、`?oidc_error=` 展示原因；
 * 后端开启 SSO 时显隐「企业账号登录」按钮（整页跳转 /auth/oidc/login 走 IdP）。
 */

import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { Bird } from 'lucide-react';
import { Button, buttonVariants } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { setAuth, type AuthUser } from '@/lib/auth';
import { getOidcStatus, postLogin, postOidcToken } from '@/api/manual-body';

const loginSchema = z.object({
  username: z.string().min(1, '请输入用户名'),
  password: z.string().min(1, '请输入密码'),
});

type LoginFormValues = z.infer<typeof loginSchema>;

export function LoginPage() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [error, setError] = useState<string | null>(null);
  const [oidcReady, setOidcReady] = useState(false);
  const [exchanging, setExchanging] = useState(false);

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

  // M13：SSO 回跳（一次性 code 兑换 / 错误提示）；先清 URL 参数防刷新重复兑换。
  // ref 兜住 React StrictMode 双执行 effect——code 一次性，重复兑换必然 AUTH_010。
  const oidcExchanged = useRef(false);
  useEffect(() => {
    const code = searchParams.get('oidc');
    const oidcError = searchParams.get('oidc_error');
    if ((!code && !oidcError) || oidcExchanged.current) return;
    oidcExchanged.current = true;
    const rest = new URLSearchParams(searchParams);
    rest.delete('oidc');
    rest.delete('oidc_error');
    setSearchParams(rest, { replace: true });
    if (oidcError) {
      setError(`企业账号登录失败：${oidcError}`);
      return;
    }
    setExchanging(true);
    void (async () => {
      try {
        const { data, response } = await postOidcToken(code ?? '');
        if (!response.ok || !data?.token) {
          setError('企业账号登录已过期，请重新发起登录');
          return;
        }
        const user: AuthUser = {
          user_id: data.user_id ?? 0,
          username: data.username ?? '',
          is_admin: data.is_admin ?? false,
          must_change_password: data.must_change_password ?? false,
        };
        setAuth(data.token, user);
        navigate(user.must_change_password ? '/change-password' : '/kbs', { replace: true });
      } catch (e) {
        setError(e instanceof Error ? e.message : '企业账号登录失败，请重新发起登录');
      } finally {
        setExchanging(false);
      }
    })();
    // 仅在挂载时处理一次回跳参数（code 一次性，重复执行无意义）
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // M13：SSO 可用性（公开 status 端点；未启用/未部署则不显示按钮）
  useEffect(() => {
    void (async () => {
      try {
        const { data } = await getOidcStatus();
        setOidcReady(data?.enabled === true);
      } catch {
        /* 后端版本不支持 SSO → 静默隐藏 */
      }
    })();
  }, []);

  const submitting = form.formState.isSubmitting;

  return (
    <div className="flex min-h-svh items-center justify-center bg-[var(--background)] px-4">
      <div className="w-full max-w-sm rounded-[var(--radius-lg)] border border-[var(--border)] bg-[var(--card)] p-8 shadow-[var(--shadow)]">
        <div
          className="mx-auto mb-4 flex size-11 items-center justify-center rounded-[var(--radius)] bg-[var(--accent)] text-[var(--on-accent)]"
          aria-hidden="true"
        >
          <Bird className="size-5" />
        </div>
        <h1 className="text-center text-xl font-semibold">啄木鸟 Woodpecker</h1>
        <p className="mb-6 mt-1 text-center text-[13px] text-[var(--muted)]">企业内部知识库问答系统</p>

        {exchanging ? (
          <p className="mb-4 text-center text-sm text-[var(--muted)]">正在完成企业账号登录…</p>
        ) : null}

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

          <Button type="submit" className="w-full" disabled={submitting || exchanging}>
            {submitting ? '登录中…' : '登录'}
          </Button>
        </form>

        {oidcReady ? (
          <>
            <div className="mt-5 flex items-center gap-3 text-[11px] text-[var(--faint)]">
              <span className="h-px flex-1 bg-[var(--border)]" />
              或
              <span className="h-px flex-1 bg-[var(--border)]" />
            </div>
            <a
              href="/api/v1/auth/oidc/login"
              className={buttonVariants({ variant: 'outline' }) + ' w-full'}
              data-testid="oidc-login"
            >
              企业账号登录（SSO）
            </a>
          </>
        ) : null}

        <p className="mt-4 text-center text-xs text-[var(--faint)]">
          账号由管理员统一创建 · 忘记密码请联系管理员
        </p>
      </div>
    </div>
  );
}
