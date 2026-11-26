/**
 * 系统设置（FR-13，/admin/settings）：排除规则编辑（glob 每行一条，支持恢复默认清单）
 * + 自助注册开关 toggle。保存走 PATCH（记审计 setting.update），成功后 invalidate。
 */

import { useEffect, useState } from 'react';
import { RotateCcw, Save } from 'lucide-react';
import { toast } from 'sonner';
import { QueryBoundary } from '@/components/states/query-boundary';
import { Skeleton } from '@/components/ui/skeleton';
import { PageHeader } from '@/components/shared/page-header';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { useAdminSettings, usePatchSettings } from '@/hooks/use-admin';
import { ThemeSettingsSection } from '@/components/theme/theme-controls';

export function AdminSettingsPage() {
  const settings = useAdminSettings();
  const patchSettings = usePatchSettings();

  const [rulesText, setRulesText] = useState('');
  const [selfRegister, setSelfRegister] = useState(false);
  const [loaded, setLoaded] = useState(false);

  // 数据到达后同步一次表单（后续以本地编辑态为准，避免保存后被 refetch 覆盖输入）
  useEffect(() => {
    if (settings.data && !loaded) {
      setRulesText((settings.data.exclusion_rules ?? []).join('\n'));
      setSelfRegister(settings.data.self_register === true);
      setLoaded(true);
    }
  }, [settings.data, loaded]);

  const handleSave = () => {
    const rules = rulesText
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line.length > 0);
    patchSettings.mutate(
      { exclusion_rules: rules, self_register: selfRegister },
      {
        onSuccess: () => toast.success('设置已保存（已记入审计）'),
        onError: (e) => toast.error(e instanceof Error ? e.message : '保存失败'),
      },
    );
  };

  const handleResetRules = () => {
    // 传空数组=清空自定义，回到内置默认清单（后端语义）
    patchSettings.mutate(
      { exclusion_rules: [] },
      {
        onSuccess: () => {
          toast.success('已恢复内置默认排除清单');
          setRulesText('');
          setLoaded(false);
        },
        onError: (e) => toast.error(e instanceof Error ? e.message : '操作失败'),
      },
    );
  };

  return (
    <div className="mx-auto w-full max-w-3xl">
      <PageHeader
        title="系统设置"
        description="外观偏好、摄取排除规则与自助注册开关；数据项每次保存均记录审计（setting.update）。"
      />

      {/* 外观设置（主题/配色为本地浏览器偏好，不走服务端） */}
      <section className="kb-card mb-6">
        <div className="mb-3">
          <h2 className="text-base font-semibold">外观设置</h2>
          <p className="text-xs text-[var(--muted)]">
            明暗主题与配色方案，保存于当前浏览器，立即生效。
          </p>
        </div>
        <ThemeSettingsSection />
      </section>

      <QueryBoundary
        query={settings}
        skeleton={
          <div className="space-y-3">
            <Skeleton className="h-8 w-1/3" />
            <Skeleton className="h-40 w-full" />
            <Skeleton className="h-10 w-2/3" />
          </div>
        }
      >
        {(data) => (
          <div className="space-y-6">
            {/* 排除规则 */}
            <section className="kb-card">
              <div className="mb-3 flex items-center justify-between">
                <div>
                  <h2 className="text-base font-semibold">摄取排除规则</h2>
                  <p className="text-xs text-[var(--muted)]">
                    glob 模式，每行一条；命中文件不入库（如 README、锁文件）。
                  </p>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={handleResetRules}
                  disabled={patchSettings.isPending}
                  title="清空自定义规则，恢复内置默认清单"
                >
                  <RotateCcw aria-hidden="true" />
                  恢复默认清单
                </Button>
              </div>
              <Textarea
                value={rulesText}
                onChange={(e) => setRulesText(e.target.value)}
                rows={8}
                className="resize-y font-mono text-xs"
                placeholder={'**/README*\n**/package-lock.json\n**/*.min.js'}
                aria-label="排除规则（每行一条 glob）"
                data-testid="exclusion-rules"
              />
              {data.exclusion_rules_customized === false ? (
                <p className="mt-2 text-xs text-[var(--faint)]">
                  当前使用内置默认清单（未自定义）；编辑保存后即覆盖为自定义清单。
                </p>
              ) : (
                <p className="mt-2 text-xs text-[var(--faint)]">
                  当前为自定义清单；清空并保存（或点「恢复默认清单」）可回到内置默认。
                </p>
              )}
            </section>

            {/* 自助注册开关 */}
            <section className="kb-card">
              <div className="flex items-center justify-between">
                <div>
                  <h2 className="text-base font-semibold">自助注册</h2>
                  <p className="text-xs text-[var(--muted)]">
                    开启后登录页允许自助创建账号（默认关闭，FR-01）。
                  </p>
                </div>
                <label className="flex cursor-pointer items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={selfRegister}
                    onChange={(e) => setSelfRegister(e.target.checked)}
                    className="accent-[var(--primary)]"
                    data-testid="self-register-toggle"
                  />
                  {selfRegister ? '已开启' : '已关闭'}
                </label>
              </div>
            </section>

            <div className="flex justify-end">
              <Button onClick={handleSave} disabled={patchSettings.isPending} data-testid="save-settings">
                <Save aria-hidden="true" />
                {patchSettings.isPending ? '保存中…' : '保存设置'}
              </Button>
            </div>
          </div>
        )}
      </QueryBoundary>
    </div>
  );
}
