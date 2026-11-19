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
}

export interface Items<T> {
  items: T[]
}
