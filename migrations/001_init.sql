CREATE TABLE IF NOT EXISTS indexed_deposits (
    deposit_id TEXT PRIMARY KEY,
    owner_address TEXT NOT NULL,
    denom TEXT NOT NULL,
    amount TEXT NOT NULL,
    processed BOOLEAN NOT NULL DEFAULT FALSE,
    created_height BIGINT NOT NULL DEFAULT 0,
    tx_hash TEXT,
    source TEXT NOT NULL DEFAULT 'chain',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_indexed_deposits_owner
    ON indexed_deposits(owner_address);

CREATE INDEX IF NOT EXISTS idx_indexed_deposits_processed
    ON indexed_deposits(processed);

CREATE TABLE IF NOT EXISTS withdraw_requests (
    withdraw_id TEXT PRIMARY KEY,
    owner_address TEXT NOT NULL,
    denom TEXT NOT NULL,
    amount TEXT NOT NULL,
    destination_address TEXT NOT NULL,
    nonce TEXT NOT NULL,
    signature TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'requested',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_withdraw_requests_owner
    ON withdraw_requests(owner_address);

CREATE INDEX IF NOT EXISTS idx_withdraw_requests_status
    ON withdraw_requests(status);

CREATE TABLE IF NOT EXISTS batch_builds (
    batch_id TEXT PRIMARY KEY,
    old_state_root TEXT NOT NULL,
    new_state_root TEXT NOT NULL,
    settlement_update JSONB NOT NULL,
    batch_commitments JSONB NOT NULL,
    witness JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'built',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_batch_builds_status
    ON batch_builds(status);

CREATE TABLE IF NOT EXISTS proof_bundles (
    batch_id TEXT PRIMARY KEY,
    proof TEXT NOT NULL,
    public_inputs JSONB NOT NULL,
    verification_key_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ready',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS submit_batches (
    batch_id TEXT PRIMARY KEY,
    tx_hash TEXT,
    accepted BOOLEAN NOT NULL DEFAULT FALSE,
    proof_status TEXT NOT NULL DEFAULT 'pending',
    error_message TEXT,
    submitted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_submit_batches_tx_hash
    ON submit_batches(tx_hash);

CREATE TABLE IF NOT EXISTS indexed_withdraw_records (
    withdraw_id TEXT PRIMARY KEY,
    owner_address TEXT NOT NULL,
    denom TEXT NOT NULL,
    amount TEXT NOT NULL,
    destination_address TEXT NOT NULL,
    nullifier TEXT NOT NULL,
    claimed BOOLEAN NOT NULL DEFAULT FALSE,
    tx_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_indexed_withdraw_records_nullifier
    ON indexed_withdraw_records(nullifier);

CREATE INDEX IF NOT EXISTS idx_indexed_withdraw_records_owner
    ON indexed_withdraw_records(owner_address);

CREATE TABLE IF NOT EXISTS chain_indexer_checkpoints (
    name TEXT PRIMARY KEY,
    last_block_height BIGINT NOT NULL DEFAULT 0,
    last_tx_hash TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);