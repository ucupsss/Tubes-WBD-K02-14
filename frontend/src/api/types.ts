// Bentuk data API (lihat internal/api di backend). Nama field snake_case persis seperti JSON server.

export interface UserRef {
  id: number
  username: string
  is_admin: boolean
}

export interface GroupRef {
  id: number
  name: string
}

export interface Me {
  user: UserRef
  csrf_token: string
  led_groups: GroupRef[]
}

export interface LoginResult {
  user: UserRef
  csrf_token: string
}

export interface AdminUser {
  id: number
  username: string
  is_admin: boolean
  is_active: boolean
  created_at: string
}

export interface Group {
  id: number
  name: string
  description: string
  created_at: string
  member_count: number
  leader_id: number | null
  leader_name: string
}

export interface GroupMember {
  user_id: number
  username: string
  is_active: boolean
  is_leader: boolean
}

export interface GroupCandidate {
  id: number
  username: string
}

export interface GroupDetail {
  group: Group
  members: GroupMember[]
  candidates: GroupCandidate[]
}

export type AccessLevel = 'none' | 'view' | 'edit'

export interface DatabaseGrant {
  database_id: number
  database_name: string
  level: AccessLevel
}

export interface Connection {
  id: number
  name: string
  host: string
  port: number
  db_name: string
  username: string
  sslmode: string
  created_at: string
  updated_at: string
}

export interface ConnectionInput {
  name: string
  host: string
  port: number
  db_name: string
  username: string
  password: string
  sslmode: string
}

export interface TestResult {
  ok: boolean
  message: string
}

export interface PendingApproval {
  execution_id: number
  round: number
  group_id: number
  group_name: string
  created_at: string
}

export interface AccessibleDatabase {
  id: number
  name: string
  level: 'view' | 'edit'
  host?: string
  port?: number
  db_name?: string
}

export interface Items<T> {
  items: T[]
}

// --- Explorer & Explain (B3, B4) ---

export interface TableSummary {
  schema: string
  name: string
  type: string
  approx_rows: number
}

export interface SchemaOverview {
  database: AccessibleDatabase
  tables: TableSummary[]
}

export interface ColumnInfo {
  name: string
  data_type: string
  is_nullable: boolean
  default_value: string | null
  is_primary_key: boolean
}

export interface ConstraintInfo {
  name: string
  type: string
  definition: string
  foreign_table?: string
  foreign_column?: string
}

export interface IndexInfo {
  name: string
  definition: string
  is_unique: boolean
  is_primary: boolean
}

export interface TableMetadata {
  database_id: number
  schema: string
  table: string
  columns: ColumnInfo[]
  constraints: ConstraintInfo[]
  indexes: IndexInfo[]
}

export interface TableMetadataResponse {
  database: AccessibleDatabase
  metadata: TableMetadata
}

export interface TableDataPage {
  database_id: number
  schema: string
  table: string
  columns: string[]
  rows: string[][]
  total_rows: number
  page: number
  size: number
  total_pages: number
  has_next: boolean
  has_prev: boolean
  prev_page: number
  next_page: number
  pk_columns: string[]
}

export interface TableDataResponse {
  database: AccessibleDatabase
  data_page: TableDataPage
  items: string[][]
  page: number
  page_size: number
  total: number
}

export interface ExplainNode {
  node_type: string
  operation?: string
  relation_name?: string
  schema?: string
  alias?: string
  index_name?: string
  join_type?: string
  strategy?: string
  scan_direction?: string
  condition?: string
  startup_cost: number
  total_cost: number
  plan_rows: number
  plan_width: number
  actual_startup_time: number
  actual_total_time: number
  actual_rows: number
  actual_loops: number
  rows_removed: number
  exclusive_ms: number
  slow: boolean
  misestimated: boolean
  estimate_ratio: number
  children?: ExplainNode[]
}

export interface ExplainPlan {
  root: ExplainNode
  planning_ms: number
  execution_ms: number
  analyzed: boolean
  slow_nodes: number
  misestimates: number
  scan_summary: string[]
  join_summary: string[]
}

export interface ExplainStatementResult {
  index: number
  sql: string
  kind: string
  plan?: ExplainPlan
  note?: string
  error?: string
}

export interface ExplainReport {
  analyze: boolean
  statements: ExplainStatementResult[]
  failed: number
}

// --- Executions, History, Jobs & Batches (B5, C1, C2) ---

export type ExecutionKind = 'apply' | 'forward' | 'rollback'

export type ExecutionStatus =
  | 'pending_review'
  | 'approved'
  | 'queued'
  | 'running'
  | 'success'
  | 'failed'
  | 'partial'
  | 'partially_reverted'
  | 'uncertain'
  | 'rejected'
  | 'cancelled'

export interface ExecutionItem {
  id: number
  database_id: number
  migration_id: number
  migration_title?: string
  revision_id: number
  revision_no?: number
  kind: ExecutionKind
  status: ExecutionStatus
  requested_by: number
  requested_by_username?: string
  rolls_back_execution_id?: number | null
  approval_round: number
  version_applied?: number | null
  batches_committed: number
  rows_committed: number
  exec_mode?: 'normal' | 'batch'
  irreversible?: boolean
  irreversible_reason?: string | null
  is_top_of_chain?: boolean
  up_sql?: string
  down_sql?: string | null
  created_at: string
  started_at?: string | null
  finished_at?: string | null
  error_code?: string | null
  error_message?: string | null
}

export interface ExecutionApprovalItem {
  execution_id: number
  round: number
  group_id: number
  group_name: string
  leader_id?: number | null
  leader_name?: string
  decision: 'pending' | 'approved' | 'rejected'
  comment?: string
  decided_by_name?: string
  decided_at?: string | null
}

export interface ExecutionAttemptItem {
  id: number
  execution_id: number
  attempt_no: number
  worker_id?: string | null
  status: 'running' | 'success' | 'failed' | 'uncertain'
  sql_executed: string
  sqlstate?: string | null
  error_message?: string | null
  started_at: string
  finished_at?: string | null
  trace_id?: string | null
}

export interface ExecutionBatchItem {
  execution_id: number
  direction: 'apply' | 'revert'
  batch_no: number
  from_value: number
  to_value: number
  rows_affected: number
  committed_at: string
}

export interface ExecutionDetailResponse {
  execution: ExecutionItem
  approvals: ExecutionApprovalItem[]
  attempts: ExecutionAttemptItem[]
  batches: ExecutionBatchItem[]
  job_id?: number | null
  can_edit?: boolean
}

export interface AsyncJobCreated {
  job_id: number
  execution_id: number
}

export interface JobStatusResponse {
  id?: number
  job_id?: number
  execution_id: number
  status: 'queued' | 'running' | 'done' | 'dead'
  execution_status: ExecutionStatus
  batches_committed: number
  rows_committed: number
  attempts?: number
  queue_wait_ms?: number
  error_message?: string | null
}

// --- Definisi migration (A6, API milik C1) ---

export type ExecMode = 'normal' | 'batch'

export interface MigrationRevision {
  id: number
  revision_no: number
  up_sql: string
  down_sql: string | null
  irreversible: boolean
  irreversible_reason: string | null
  exec_mode: ExecMode
  batch_table: string | null
  batch_column: string | null
  batch_size: number | null
  sql_hash: string
  /** Terisi setelah revisi pertama kali diajukan; sejak itu isi SQL-nya tidak bisa diubah. */
  frozen_at: string | null
}

export interface MigrationItem {
  id: number
  database_id: number
  title: string
  /** Posisi di rantai aktif; null bila belum (atau tidak lagi) aktif. */
  version: number | null
  created_by: number
  created_by_username: string
  created_at: string
  revision: MigrationRevision
}

export interface MigrationInput {
  title: string
  up_sql: string
  down_sql: string
  irreversible: boolean
  irreversible_reason: string
  exec_mode: ExecMode
  batch_table: string
  batch_column: string
  batch_size: number
}

export interface Paged<T> {
  items: T[]
  page: number
  page_size: number
  total: number
}
