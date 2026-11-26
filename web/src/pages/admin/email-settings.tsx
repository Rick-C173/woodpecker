/**
 * 邮件配置（FR-16，/admin/email）：SMTP 发件服务器设置 + 测试发送。
 * 密码不回显（保存时空=不修改）；测试发送验证连通性。
 */

import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  getEmailSettings,
  patchEmailSettings,
  postEmailTest,
  type EmailSettings,
} from '@/api/manual-body';

function toastError(e: unknown): void {
  toast.error(e instanceof Error ? e.message : '操作失败，请稍后重试');
}

export function AdminEmailSettingsPage() {
  const [cfg, setCfg] = useState<EmailSettings>({ host: '', port: 587, username: '', from: '' });
  const [password, setPassword] = useState('');
  const [testTo, setTestTo] = useState('');
  const [hasPassword, setHasPassword] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);

  useEffect(() => {
    void (async () => {
      try {
        const { data, response } = await getEmailSettings();
        if (!response.ok) return;
        if (data && 'configured' in data && data.configured === false) return;
        if (data) {
          setCfg({ host: data.host, port: data.port, username: data.username, from: data.from });
          setHasPassword(true);
        }
      } catch (e) {
        toastError(e);
      }
    })();
  }, []);

  const handleSave = async () => {
    if (!cfg.host.trim() || cfg.port <= 0 || !cfg.from.trim()) {
      toast.error('服务器、端口与发件人必填');
      return;
    }
    setSaving(true);
    try {
      const { response } = await patchEmailSettings({ ...cfg, password: password || undefined });
      if (!response.ok) {
        toast.error('保存失败');
        return;
      }
      toast.success('邮件配置已保存');
      setPassword('');
      setHasPassword(true);
    } catch (e) {
      toastError(e);
    } finally {
      setSaving(false);
    }
  };

  const handleTest = async () => {
    if (!testTo.trim()) {
      toast.error('请填写测试收件邮箱');
      return;
    }
    setTesting(true);
    try {
      const { response } = await postEmailTest(testTo.trim());
      if (!response.ok) {
        toast.error('测试发送失败（见错误提示）');
        return;
      }
      toast.success('测试邮件已发送，请查收');
    } catch (e) {
      toastError(e);
    } finally {
      setTesting(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold">邮件配置</h1>
        <p className="mt-1 text-sm text-[var(--faint)]">
          配置 SMTP 发件服务器后即可发送邀请邮件。端口 465 使用隐式 TLS，587/25 自动升级 STARTTLS。
        </p>
      </div>

      <section className="max-w-xl space-y-4 rounded-lg border border-[var(--border)] p-4">
        <div className="grid gap-2">
          <Label htmlFor="email-host">SMTP 服务器</Label>
          <Input
            id="email-host"
            value={cfg.host}
            onChange={(e) => setCfg({ ...cfg, host: e.target.value })}
            placeholder="smtp.corp.com"
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="email-port">端口（465 / 587 / 25）</Label>
          <Input
            id="email-port"
            type="number"
            value={cfg.port}
            onChange={(e) => setCfg({ ...cfg, port: Number(e.target.value) })}
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="email-username">认证账号（可空）</Label>
          <Input
            id="email-username"
            value={cfg.username}
            onChange={(e) => setCfg({ ...cfg, username: e.target.value })}
            placeholder="noreply@corp.com"
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="email-password">
            密码 {hasPassword && <span className="text-xs text-[var(--faint)]">（已保存，留空则不修改）</span>}
          </Label>
          <Input
            id="email-password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={hasPassword ? '••••••••' : 'SMTP 认证密码'}
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="email-from">发件人</Label>
          <Input
            id="email-from"
            value={cfg.from}
            onChange={(e) => setCfg({ ...cfg, from: e.target.value })}
            placeholder="Woodpecker <noreply@corp.com>"
          />
        </div>
        <Button onClick={handleSave} disabled={saving}>
          {saving ? '保存中…' : '保存配置'}
        </Button>
      </section>

      <section className="max-w-xl space-y-3 rounded-lg border border-[var(--border)] p-4">
        <h2 className="text-sm font-semibold">测试发送</h2>
        <div className="grid gap-2">
          <Label htmlFor="email-test-to">测试收件邮箱</Label>
          <div className="flex gap-2">
            <Input
              id="email-test-to"
              value={testTo}
              onChange={(e) => setTestTo(e.target.value)}
              placeholder="you@corp.com"
            />
            <Button variant="outline" onClick={handleTest} disabled={testing}>
              {testing ? '发送中…' : '发送测试'}
            </Button>
          </div>
        </div>
      </section>
    </div>
  );
}
