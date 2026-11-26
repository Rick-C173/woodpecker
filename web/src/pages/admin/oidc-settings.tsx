/**
 * SSO 登录配置（FR-19，/admin/oidc）：OIDC IdP 连接参数 + 连接测试。
 * client_secret 不回显（保存时留空=不修改）；groups_claim 默认 groups；
 * admin_claim 留空=不映射管理员（仅对 JIT 新用户生效）。保存记审计。
 */

import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api } from '@/api/client';

type OidcSettingsReq = Partial<{
  enabled: boolean;
  issuer: string;
  client_id: string;
  client_secret: string;
  redirect_uri: string;
  frontend_uri: string;
  scopes: string[];
  groups_claim: string;
  admin_claim: string;
  admin_claim_value: string;
}>;

interface OidcSettingsView {
  enabled?: boolean;
  issuer?: string;
  client_id?: string;
  redirect_uri?: string;
  frontend_uri?: string;
  scopes?: string[];
  groups_claim?: string;
  admin_claim?: string;
  admin_claim_value?: string;
  configured?: boolean;
}

interface OidcTestResult {
  issuer?: string;
  authorization_endpoint?: string;
  token_endpoint?: string;
  userinfo_endpoint?: string;
}

function toastError(e: unknown): void {
  toast.error(e instanceof Error ? e.message : '操作失败，请稍后重试');
}

export function AdminOidcSettingsPage() {
  const [cfg, setCfg] = useState<OidcSettingsView>({
    enabled: false,
    issuer: '',
    client_id: '',
    redirect_uri: '',
    frontend_uri: '',
    scopes: [],
    groups_claim: 'groups',
    admin_claim: '',
    admin_claim_value: '',
  });
  const [secret, setSecret] = useState('');
  const [hasSecret, setHasSecret] = useState(false);
  const [scopesText, setScopesText] = useState('');
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<OidcTestResult | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const { data, response } = await api.GET('/admin/oidc-settings');
        if (!response.ok || !data) return;
        if (data.configured === false) return; // 从未配置过 → 保持表单默认值
        setCfg(data);
        setHasSecret(true);
        setScopesText((data.scopes ?? []).join(' '));
      } catch (e) {
        toastError(e);
      }
    })();
  }, []);

  const handleSave = async () => {
    if (!cfg.issuer?.trim() || !cfg.client_id?.trim()) {
      toast.error('Issuer 与 Client ID 必填');
      return;
    }
    if (!cfg.redirect_uri?.trim() || !cfg.frontend_uri?.trim()) {
      toast.error('回调地址与前端地址必填');
      return;
    }
    if (!hasSecret && !secret.trim()) {
      toast.error('首次配置需填写 Client Secret');
      return;
    }
    setSaving(true);
    try {
      const scopes = scopesText
        .split(/[\s,]+/)
        .map((s) => s.trim())
        .filter((s) => s.length > 0);
      const { response } = await api.PATCH('/admin/oidc-settings', {
        body: {
          enabled: cfg.enabled === true,
          issuer: cfg.issuer.trim(),
          client_id: cfg.client_id.trim(),
          client_secret: secret || undefined,
          redirect_uri: cfg.redirect_uri.trim(),
          frontend_uri: cfg.frontend_uri.trim(),
          scopes,
          groups_claim: cfg.groups_claim?.trim() || 'groups',
          admin_claim: cfg.admin_claim?.trim(),
          admin_claim_value: cfg.admin_claim_value?.trim(),
        } satisfies OidcSettingsReq,
      });
      if (!response.ok) {
        toast.error('保存失败');
        return;
      }
      toast.success('SSO 配置已保存');
      setSecret('');
      setHasSecret(true);
    } catch (e) {
      toastError(e);
    } finally {
      setSaving(false);
    }
  };

  const handleTest = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      const { data, response } = await api.POST('/admin/oidc-settings/test');
      if (!response.ok || !data) {
        toast.error('连接失败（检查 Issuer 与网络可达性）');
        return;
      }
      setTestResult(data);
      toast.success('IdP 发现端点可达');
    } catch (e) {
      toastError(e);
    } finally {
      setTesting(false);
    }
  };

  return (
    <div className="mx-auto w-full max-w-3xl">
      <div className="mb-4">
        <h1 className="text-lg font-semibold">SSO 登录（OIDC）</h1>
        <p className="text-sm text-[var(--muted)]">
          配置企业 IdP（如 Keycloak / 飞书 / 钉钉）后，登录页显示「企业账号登录」；首次登录按邮箱
          JIT 建号，组 claim 自动同步到用户组（仅自动组）。
        </p>
      </div>

      <section className="kb-card mb-6 space-y-4">
        <div className="flex items-center justify-between border-b border-[var(--border)] pb-3">
          <div>
            <h2 className="text-base font-semibold">启用状态</h2>
            <p className="text-xs text-[var(--muted)]">
              关闭后登录页隐藏 SSO 入口；配置保留不清空。
            </p>
          </div>
          <label className="flex cursor-pointer items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={cfg.enabled === true}
              onChange={(e) => setCfg({ ...cfg, enabled: e.target.checked })}
              className="accent-[var(--primary)]"
              data-testid="oidc-enabled-toggle"
            />
            {cfg.enabled ? '已启用' : '已关闭'}
          </label>
        </div>

        <div className="grid gap-2">
          <Label htmlFor="oidc-issuer">Issuer *</Label>
          <Input
            id="oidc-issuer"
            value={cfg.issuer ?? ''}
            onChange={(e) => setCfg({ ...cfg, issuer: e.target.value })}
            placeholder="https://sso.corp.com/realms/woodpecker"
          />
          <p className="text-xs text-[var(--faint)]">
            IdP 发现地址，后端读取 {'{issuer}'}/.well-known/openid-configuration。
          </p>
        </div>

        <div className="grid gap-2">
          <Label htmlFor="oidc-client-id">Client ID *</Label>
          <Input
            id="oidc-client-id"
            value={cfg.client_id ?? ''}
            onChange={(e) => setCfg({ ...cfg, client_id: e.target.value })}
            placeholder="woodpecker"
          />
        </div>

        <div className="grid gap-2">
          <Label htmlFor="oidc-client-secret">
            Client Secret{' '}
            {hasSecret && <span className="text-xs text-[var(--faint)]">（已保存，留空则不修改）</span>}
          </Label>
          <Input
            id="oidc-client-secret"
            type="password"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            placeholder={hasSecret ? '••••••••' : 'IdP 客户端密钥'}
          />
        </div>

        <div className="grid gap-2">
          <Label htmlFor="oidc-redirect-uri">回调地址 *</Label>
          <Input
            id="oidc-redirect-uri"
            value={cfg.redirect_uri ?? ''}
            onChange={(e) => setCfg({ ...cfg, redirect_uri: e.target.value })}
            placeholder={`${window.location.origin}/api/v1/auth/oidc/callback`}
          />
          <p className="text-xs text-[var(--faint)]">
            填后端回调端点，且须与 IdP 侧登记一致。
          </p>
        </div>

        <div className="grid gap-2">
          <Label htmlFor="oidc-frontend-uri">前端地址 *</Label>
          <Input
            id="oidc-frontend-uri"
            value={cfg.frontend_uri ?? ''}
            onChange={(e) => setCfg({ ...cfg, frontend_uri: e.target.value })}
            placeholder={window.location.origin}
          />
          <p className="text-xs text-[var(--faint)]">
            登录完成后的跳转目标（登录页将携带一次性 code 自动兑换）。
          </p>
        </div>

        <div className="grid gap-2">
          <Label htmlFor="oidc-scopes">Scopes</Label>
          <Input
            id="oidc-scopes"
            value={scopesText}
            onChange={(e) => setScopesText(e.target.value)}
            placeholder="openid profile email"
          />
          <p className="text-xs text-[var(--faint)]">空格/逗号分隔，缺省 openid profile email。</p>
        </div>

        <div className="grid gap-2">
          <Label htmlFor="oidc-groups-claim">组 Claim</Label>
          <Input
            id="oidc-groups-claim"
            value={cfg.groups_claim ?? ''}
            onChange={(e) => setCfg({ ...cfg, groups_claim: e.target.value })}
            placeholder="groups"
          />
          <p className="text-xs text-[var(--faint)]">
            组名数组所在 claim，缺省 groups；命中的组自动创建并同步成员（仅自动组）。
          </p>
        </div>

        <div className="grid grid-cols-2 gap-3">
          <div className="grid gap-2">
            <Label htmlFor="oidc-admin-claim">管理员 Claim</Label>
            <Input
              id="oidc-admin-claim"
              value={cfg.admin_claim ?? ''}
              onChange={(e) => setCfg({ ...cfg, admin_claim: e.target.value })}
              placeholder="roles（留空=不映射）"
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="oidc-admin-claim-value">管理员 Claim 值</Label>
            <Input
              id="oidc-admin-claim-value"
              value={cfg.admin_claim_value ?? ''}
              onChange={(e) => setCfg({ ...cfg, admin_claim_value: e.target.value })}
              placeholder="woodpecker-admin"
            />
          </div>
        </div>
        <p className="text-xs text-[var(--faint)]">
          管理员映射仅对 SSO 首次建号的用户生效；已有账号绑定后角色不变。
        </p>

        <div className="flex justify-end">
          <Button onClick={handleSave} disabled={saving} data-testid="save-oidc">
            {saving ? '保存中…' : '保存配置'}
          </Button>
        </div>
      </section>

      <section className="kb-card space-y-3">
        <h2 className="text-sm font-semibold">连接测试</h2>
        <p className="text-xs text-[var(--muted)]">
          拉取 IdP 发现文档验证可达性（使用已保存配置，无需先保存修改）。
        </p>
        <Button variant="outline" onClick={handleTest} disabled={testing} data-testid="test-oidc">
          {testing ? '测试中…' : '测试连接'}
        </Button>
        {testResult ? (
          <div className="space-y-1 rounded-[var(--radius)] border border-[var(--border)] px-3 py-2 text-xs">
            <p>issuer：{testResult.issuer}</p>
            <p>authorization_endpoint：{testResult.authorization_endpoint}</p>
            <p>token_endpoint：{testResult.token_endpoint}</p>
            <p>userinfo_endpoint：{testResult.userinfo_endpoint}</p>
          </div>
        ) : null}
      </section>
    </div>
  );
}
