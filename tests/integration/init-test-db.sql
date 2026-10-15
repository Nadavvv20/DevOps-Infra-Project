-- Initialize Schema for Local Integration Testing
CREATE TABLE IF NOT EXISTS products (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    price_usd_units BIGINT NOT NULL,
    price_usd_nanos INT NOT NULL
);

CREATE TABLE IF NOT EXISTS inventory (
    product_id VARCHAR(64) PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
    total_stock INT NOT NULL DEFAULT 10,
    reserved_stock INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS orders (
    order_id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL,
    email VARCHAR(255),
    user_currency VARCHAR(8) NOT NULL,
    total_cost_units BIGINT NOT NULL,
    total_cost_nanos INT NOT NULL,
    shipping_tracking_id VARCHAR(64),
    status VARCHAR(32) DEFAULT 'CONFIRMED',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS order_items (
    item_id SERIAL PRIMARY KEY,
    order_id VARCHAR(64) REFERENCES orders(order_id) ON DELETE CASCADE,
    product_id VARCHAR(64) REFERENCES products(id),
    quantity INT NOT NULL
);

CREATE TABLE IF NOT EXISTS outbox (
    id VARCHAR(64) PRIMARY KEY,
    event_type VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    published_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox(published_at) WHERE published_at IS NULL;

INSERT INTO products (id, name, price_usd_units, price_usd_nanos) VALUES
('OLJCESPC7Z', 'Sunglasses', 19, 990000000),
('66VCHSJNUP', 'Tank Top', 18, 990000000),
('1YMWWN1N4O', 'Watch', 109, 990000000)
ON CONFLICT (id) DO NOTHING;

INSERT INTO inventory (product_id, total_stock, reserved_stock) VALUES
('OLJCESPC7Z', 10, 0),
('66VCHSJNUP', 10, 0),
('1YMWWN1N4O', 10, 0)
ON CONFLICT (product_id) DO NOTHING;
