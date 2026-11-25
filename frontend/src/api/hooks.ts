import { useMutation, useQuery, useQueryClient, type QueryKey } from '@tanstack/react-query'
import { api } from './client'
import type {
  AccessibleDatabase,
  AdminUser,
  Connection,
  ConnectionInput,
  DatabaseGrant,
  ExecutionDetailResponse,
  ExecutionItem,
  ExplainReport,
  Group,
  GroupDetail,
  Items,
  JobStatusResponse,
  MigrationInput,
  MigrationItem,
  Paged,
  PendingApproval,
  SchemaOverview,
  TableDataResponse,
  TableMetadataResponse,
  TestResult,
} from './types'

export const keys = {
  me: ['me'] as const,
  databases: ['databases'] as const,
  schema: (dbId: number) => ['databases', dbId, 'schema'] as const,
  tableMeta: (dbId: number, schema: string, table: string) => ['databases', dbId, 'tables', schema, table] as const,
  tableData: (dbId: number, schema: string, table: string, page: number, pageSize: number) =>
    ['databases', dbId, 'tables', schema, table, 'data', page, pageSize] as const,
  executions: (dbId: number, hideRollback: boolean) => ['databases', dbId, 'executions', hideRollback] as const,
  execution: (execId: number) => ['executions', execId] as const,
  job: (jobId: number) => ['jobs', jobId] as const,
  users: ['admin', 'users'] as const,
  groups: ['admin', 'groups'] as const,
  group: (id: number) => ['admin', 'groups', id] as const,
  access: (id: number) => ['admin', 'groups', id, 'access'] as const,
  connections: ['admin', 'databases'] as const,
  connection: (id: number) => ['admin', 'databases', id] as const,
  approvals: ['approvals'] as const,
  migrations: (dbId: number) => ['databases', dbId, 'migrations'] as const,
  migration: (dbId: number, id: number) => ['databases', dbId, 'migrations', id] as const,
  executionsAll: (dbId: number) => ['databases', dbId, 'executions'] as const,
}

export const useAccessibleDatabases = () =>
  useQuery({ queryKey: keys.databases, queryFn: () => api.get<Items<AccessibleDatabase>>('/api/databases') })

// --- Explorer & Explain (B3, B4) ---
export const useDatabaseSchema = (dbId: number) =>
  useQuery({
    queryKey: keys.schema(dbId),
    queryFn: () => api.get<SchemaOverview>(`/api/databases/${dbId}/schema`),
    enabled: Number.isFinite(dbId) && dbId > 0,
  })

export const useTableMetadata = (dbId: number, schema: string, table: string) =>
  useQuery({
    queryKey: keys.tableMeta(dbId, schema, table),
    queryFn: () =>
      api.get<TableMetadataResponse>(
        `/api/databases/${dbId}/tables/${encodeURIComponent(schema)}/${encodeURIComponent(table)}`,
      ),
    enabled: Number.isFinite(dbId) && dbId > 0 && schema !== '' && table !== '',
  })

export const useTableData = (dbId: number, schema: string, table: string, page: number, pageSize = 50) =>
  useQuery({
    queryKey: keys.tableData(dbId, schema, table, page, pageSize),
    queryFn: () =>
      api.get<TableDataResponse>(
        `/api/databases/${dbId}/tables/${encodeURIComponent(schema)}/${encodeURIComponent(table)}/data?page=${page}&page_size=${pageSize}`,
      ),
    enabled: Number.isFinite(dbId) && dbId > 0 && schema !== '' && table !== '',
  })

export const runMigrationExplain = (dbId: number, migrationId: number, analyze: boolean) =>
  api.post<ExplainReport>(`/api/databases/${dbId}/migrations/${migrationId}/explain`, { analyze })

// --- Executions & Jobs (B5) ---
export const useDatabaseExecutions = (dbId: number, hideRollback: boolean) =>
  useQuery({
    queryKey: keys.executions(dbId, hideRollback),
    queryFn: () =>
      api.get<Items<ExecutionItem>>(
        `/api/databases/${dbId}/executions?hide_rollback=${hideRollback ? 'true' : 'false'}`,
      ),
    enabled: Number.isFinite(dbId) && dbId > 0,
  })

export const useExecutionDetail = (execId: number, pollMs?: number | false) =>
  useQuery({
    queryKey: keys.execution(execId),
    queryFn: () => api.get<ExecutionDetailResponse>(`/api/executions/${execId}`),
    enabled: Number.isFinite(execId) && execId > 0,
    refetchInterval: pollMs || false,
  })

export const useJobStatus = (jobId: number | null, pollMs: number | false = 1500) =>
  useQuery({
    queryKey: keys.job(jobId ?? 0),
    queryFn: () => api.get<JobStatusResponse>(`/api/jobs/${jobId}`),
    enabled: jobId !== null && jobId > 0,
    refetchInterval: query => {
      const st = query.state.data?.status
      const execSt = query.state.data?.execution_status
      if (st === 'done' || st === 'dead') {
        return false
      }
      if (execSt && !['queued', 'running', 'uncertain'].includes(execSt)) {
        return false
      }
      return pollMs
    },
  })

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
export function useAction<TVars = void, TResult = void>(
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

// --- Definisi migration (A6) ---
const validId = (n: number) => Number.isFinite(n) && n > 0

export const useMigrations = (dbId: number) =>
  useQuery({
    queryKey: keys.migrations(dbId),
    queryFn: () => api.get<Paged<MigrationItem>>(`/api/databases/${dbId}/migrations?page_size=200`),
    enabled: validId(dbId),
  })

export const useMigration = (dbId: number, id: number) =>
  useQuery({
    queryKey: keys.migration(dbId, id),
    queryFn: () => api.get<MigrationItem>(`/api/databases/${dbId}/migrations/${id}`),
    enabled: validId(dbId) && validId(id),
  })

/** Riwayat execution terbaru (tanpa filter Hide rollback) untuk menurunkan status tiap migration. */
export const useRecentExecutions = (dbId: number) =>
  useQuery({
    queryKey: [...keys.executionsAll(dbId), 'recent'],
    queryFn: () => api.get<Paged<ExecutionItem>>(`/api/databases/${dbId}/executions?page_size=200`),
    enabled: validId(dbId),
  })

export const createMigration = (dbId: number, input: MigrationInput) =>
  api.post<MigrationItem>(`/api/databases/${dbId}/migrations`, input)

export const updateMigration = (dbId: number, id: number, input: MigrationInput) =>
  api.put<MigrationItem>(`/api/databases/${dbId}/migrations/${id}`, input)

export const validateMigration = (dbId: number, id: number, input: MigrationInput) =>
  api.post<{ valid: boolean }>(`/api/databases/${dbId}/migrations/${id}/validate`, input)
