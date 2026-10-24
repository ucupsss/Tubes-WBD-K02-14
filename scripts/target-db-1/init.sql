-- Sample catalog for target-db-1.
-- Customers, products, orders (FK + index), and a larger page_views table for EXPLAIN demos.

CREATE TABLE customers (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT NOT NULL UNIQUE,
  city TEXT NOT NULL
);

CREATE TABLE products (
  id BIGSERIAL PRIMARY KEY,
  sku TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  price INT NOT NULL CHECK (price >= 0)
);

CREATE TABLE orders (
  id BIGSERIAL PRIMARY KEY,
  customer_id BIGINT NOT NULL REFERENCES customers (id),
  status TEXT NOT NULL CHECK (status IN ('pending', 'paid', 'shipped')),
  total INT NOT NULL CHECK (total >= 0),
  ordered_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orders_customer_id ON orders (customer_id);
CREATE INDEX idx_orders_ordered_at ON orders (ordered_at);

CREATE TABLE page_views (
  id BIGSERIAL PRIMARY KEY,
  user_id INT NOT NULL,
  path TEXT NOT NULL,
  viewed_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_page_views_user_id ON page_views (user_id);
CREATE INDEX idx_page_views_path ON page_views (path);

INSERT INTO customers (name, email, city)
SELECT
  'Customer ' || g,
  'customer' || g || '@example.com',
  (ARRAY['Jakarta', 'Bandung', 'Surabaya', 'Medan', 'Yogyakarta'])[1 + (g % 5)]
FROM generate_series(1, 20) AS g;

INSERT INTO products (sku, name, price)
SELECT
  'SKU-' || lpad(g::text, 4, '0'),
  'Product ' || g,
  10000 + (g * 500)
FROM generate_series(1, 30) AS g;

INSERT INTO orders (customer_id, status, total, ordered_at)
SELECT
  1 + (g % 20),
  (ARRAY['pending', 'paid', 'shipped'])[1 + (g % 3)],
  50000 + (g * 1000),
  TIMESTAMPTZ '2026-01-01 00:00:00+00' + (g || ' hours')::interval
FROM generate_series(1, 100) AS g;

INSERT INTO page_views (user_id, path, viewed_at)
SELECT
  1 + (g % 50),
  (ARRAY['/home', '/catalog', '/product/1', '/cart', '/checkout'])[1 + (g % 5)],
  TIMESTAMPTZ '2026-02-01 00:00:00+00' + (g || ' minutes')::interval
FROM generate_series(1, 200) AS g;
