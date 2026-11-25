/**
 * TanStack Query key 工厂（架构 §1.2）：前缀分段，与后端资源树对齐。
 * 失效纪律：mutation 成功后按资源前缀 invalidate（如 qk.kbs.documents(kbId, {})）。
 */

export const qk = {
  // 认证态由 AuthProvider 派生，非 Query
  auth: {
    session: ['auth', 'session'] as const,
  },
  kbs: {
    list: ['kbs'] as const,
    detail: (kbId: number) => ['kbs', kbId] as const,
    members: (kbId: number) => ['kbs', kbId, 'members'] as const,
    documents: (kbId: number, filters: Record<string, unknown>) =>
      ['kbs', kbId, 'documents', filters] as const,
    document: (kbId: number, docId: number) => ['kbs', kbId, 'documents', docId] as const,
    search: (kbId: number, query: string) => ['kbs', kbId, 'search', query] as const,
    conversations: (kbId: number) => ['kbs', kbId, 'conversations'] as const,
  },
  conversations: {
    detail: (cId: number) => ['conversations', cId] as const, // 消息历史
  },
  admin: {
    users: ['admin', 'users'] as const,
    models: ['admin', 'models'] as const,
    settings: ['admin', 'settings'] as const,
    audit: ['admin', 'audit'] as const,
    feedbacks: ['admin', 'feedbacks'] as const,
  },
} as const;
