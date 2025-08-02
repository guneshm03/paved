-- Initial schema for invoice-service.
CREATE TABLE IF NOT EXISTS invoice_service_events (
    id         BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload    JSONB NOT NULL
);
