/**
 * dayjs 封装：相对时间 / RFC3339 转本地。
 * 后端时间统一 RFC3339 UTC；展示转本地时间即可（utc/timezone 插件不需要）。
 */

import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';
import 'dayjs/locale/zh-cn';

dayjs.extend(relativeTime);
dayjs.locale('zh-cn');

/** 解析 RFC3339 UTC 时间串为本地 dayjs 实例。 */
export function parseTime(iso: string | null | undefined): dayjs.Dayjs | null {
  if (!iso) return null;
  const d = dayjs(iso);
  return d.isValid() ? d : null;
}

/** 相对时间（"3 分钟前" / "昨天 17:05" 风格交由 UI 层组合）。 */
export function fromNow(iso: string | null | undefined): string {
  const d = parseTime(iso);
  return d ? d.fromNow() : '';
}

/** 本地日期时间（列表/详情展示）。 */
export function formatDateTime(iso: string | null | undefined): string {
  const d = parseTime(iso);
  return d ? d.format('YYYY-MM-DD HH:mm') : '';
}

/** 本地日期。 */
export function formatDate(iso: string | null | undefined): string {
  const d = parseTime(iso);
  return d ? d.format('YYYY-MM-DD') : '';
}

export { dayjs };
