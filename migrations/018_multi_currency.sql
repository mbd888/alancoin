-- +goose Up
CREATE TABLE agent_currency_balances (
    agent_addr  VARCHAR(42) NOT NULL,
    currency    VARCHAR(10) NOT NULL,
    decimals    INTEGER NOT NULL DEFAULT 6,
    available   NUMERIC(30,18) NOT NULL DEFAULT 0,
    pending     NUMERIC(30,18) NOT NULL DEFAULT 0,
    escrowed    NUMERIC(30,18) NOT NULL DEFAULT 0,
    total_in    NUMERIC(30,18) NOT NULL DEFAULT 0,
    total_out   NUMERIC(30,18) NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (agent_addr, currency),
    CHECK (available >= 0),
    CHECK (pending >= 0),
    CHECK (escrowed >= 0)
);

CREATE TABLE exchange_rates (
    from_currency VARCHAR(10) NOT NULL,
    to_currency   VARCHAR(10) NOT NULL,
    rate          NUMERIC(30,18) NOT NULL,
    source        VARCHAR(50),
    updated_at    TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (from_currency, to_currency)
);

-- +goose Down
DROP TABLE IF EXISTS exchange_rates;
DROP TABLE IF EXISTS agent_currency_balances;
