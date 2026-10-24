-- Sample catalog for target-db-2.
-- Departments, employees (FK + index), and a larger audit_log table for EXPLAIN demos.

CREATE TABLE departments (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL UNIQUE
);

CREATE TABLE employees (
  id BIGSERIAL PRIMARY KEY,
  department_id BIGINT NOT NULL REFERENCES departments (id),
  full_name TEXT NOT NULL,
  email TEXT NOT NULL UNIQUE,
  hired_on DATE NOT NULL
);

CREATE INDEX idx_employees_department_id ON employees (department_id);

CREATE TABLE audit_log (
  id BIGSERIAL PRIMARY KEY,
  employee_id BIGINT NOT NULL REFERENCES employees (id),
  action TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_audit_log_employee_id ON audit_log (employee_id);
CREATE INDEX idx_audit_log_action ON audit_log (action);

INSERT INTO departments (name)
VALUES ('Engineering'), ('Finance'), ('Support'), ('Operations');

INSERT INTO employees (department_id, full_name, email, hired_on)
SELECT
  1 + (g % 4),
  'Employee ' || g,
  'employee' || g || '@example.com',
  DATE '2024-01-01' + g
FROM generate_series(1, 100) AS g;

INSERT INTO audit_log (employee_id, action, created_at)
SELECT
  1 + (g % 100),
  (ARRAY['login', 'export', 'update', 'view'])[1 + (g % 4)],
  TIMESTAMPTZ '2026-03-01 00:00:00+00' + (g || ' minutes')::interval
FROM generate_series(1, 250) AS g;
