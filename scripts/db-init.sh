#!/bin/sh
set -e

echo "=========================================================="
echo "Running DB Schema Migration & Seed from db-init-job.yaml..."
echo "=========================================================="

export PGPASSWORD="$DB_PASSWORD"

until pg_isready -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME"; do
  echo "Waiting for PostgreSQL at $DB_HOST:$DB_PORT to accept connections..."
  sleep 2
done

echo "Database is ready! Extracting SQL directly from db-init-job.yaml (zero forking)..."
if [ ! -f /templates/db-init-job.yaml ]; then
  echo "ERROR: /templates/db-init-job.yaml not found!"
  exit 1
fi

# Extract identical SQL between << 'EOF' and EOF directly from db-init-job.yaml
awk '/<< '\''EOF'\''/{flag=1;next}/^ *EOF$/{flag=0}flag' /templates/db-init-job.yaml | psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME"

echo "=========================================================="
echo "Verifying migrated tables and seed row counts..."
echo "=========================================================="
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -c "
SELECT 'users' AS table_name, count(*) AS row_count FROM users
UNION ALL
SELECT 'products' AS table_name, count(*) AS row_count FROM products
UNION ALL
SELECT 'inventory' AS table_name, count(*) AS row_count FROM inventory
UNION ALL
SELECT 'orders' AS table_name, count(*) AS row_count FROM orders
UNION ALL
SELECT 'order_items' AS table_name, count(*) AS row_count FROM order_items
UNION ALL
SELECT 'customers' AS table_name, count(*) AS row_count FROM customers;
"

echo "SUCCESS: Database migration and seeding completed from referenced db-init-job.yaml!"
