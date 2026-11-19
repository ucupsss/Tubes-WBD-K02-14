import { useMutation, useQuery, useQueryClient, type QueryKey } from '@tanstack/react-query'
import { api } from './client'
import type {
  AccessibleDatabase,
  AdminUser,
  Connection,
  ConnectionInput,
  DatabaseGrant,
  Group,
  GroupDetail,
  Items,
  PendingApproval,
  TestResult,
} from './types'

export const keys = {
  me: ['me'] as const,
  databases: ['databases'] as const,
  users: ['admin', 'users'] as const,
  groups: ['admin', 'groups'] as const,
  group: (id: number) => ['admin', 'groups', id] as const,
  access: (id: number) => ['admin', 'groups', id, 'access'] as const,
  connections: ['admin', 'databases'] as const,
  connection: (id: number) => ['admin', 'databases', id] as const,
  approvals: ['approvals'] as const,
}

export const useAccessibleDatabases = () =>
  useQuery({ queryKey: keys.databases, queryFn: () => api.get<Items<AccessibleDatabase>>('/api/databases') })

// --- Users ---
export const useUsers = () =>
  useQuery({ queryKey: keys.users, queryFn: () => api.get<Items<AdminUser>>('/api/admin/users') })

// --- Groups ---
export const useGroups = () =>
  useQuery({ queryKey: keys.groups, queryFn: () => api.get<Items<Group>>('/api/admin/groups') })

export const useGroup = (id: number) =>
  useQuery({ queryKey: keys.group(id), queryFn: () => api.get<GroupDetail>(`/api/admin/groups/${id}`) })

export const useGroupAccess = (id: number) =>
  useQuery({
    queryKey: keys.access(id),
    queryFn: () => api.get<Items<DatabaseGrant>>(`/api/admin/groups/${id}/access`),
  })

// --- Connections ---
export const useConnections = () =>
  useQuery({ queryKey: keys.connections, queryFn: () => api.get<Items<Connection>>('/api/admin/databases') })

export const useConnection = (id: number | null) =>
  useQuery({
    queryKey: keys.connection(id ?? 0),
    queryFn: () => api.get<Connection>(`/api/admin/databases/${id}`),
    enabled: id !== null,
  })

export const testConnection = (id: number | null, input: Partial<ConnectionInput>) =>
  api.post<TestResult>(id ? `/api/admin/databases/${id}/test` : '/api/admin/databases/test', input)

// --- Approvals ---
export const usePendingApprovals = () =>
  useQuery({
    queryKey: keys.approvals,
    queryFn: () => api.get<Items<PendingApproval>>('/api/approvals'),
  })

/**
 * Mutasi umum: jalankan fn, lalu batalkan cache query yang terkait supaya tampilan memuat ulang.
 * Error dilempar ke pemanggil (form menampilkannya per field).
 */
export function useAction<TVars, TResult = void>(
  fn: (vars: TVars) => Promise<TResult>,
  invalidate: QueryKey[] = [],
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: async () => {
      await Promise.all(invalidate.map(queryKey => qc.invalidateQueries({ queryKey })))
    },
  })
}
