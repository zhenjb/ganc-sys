CREATE TABLE IF NOT EXISTS offchain_state_cursors (
    name TEXT PRIMARY KEY,

    committed_root TEXT NOT NULL,
    pending_root TEXT NOT NULL,
    last_committed_batch_id TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO offchain_state_cursors (
    name,
    committed_root,
    pending_root
)
VALUES (
    'default',
    '0xrootA',
    '0xrootA'
)
ON CONFLICT (name) DO NOTHING;

CREATE TABLE IF NOT EXISTS offchain_pending_deposits (
    deposit_id TEXT PRIMARY KEY,

    owner_address TEXT NOT NULL,
    denom TEXT NOT NULL,
    amount TEXT NOT NULL,

    root_before TEXT NOT NULL,
    root_after TEXT NOT NULL,
    balance_before TEXT NOT NULL,
    balance_after TEXT NOT NULL,

    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'included', 'committed', 'failed')),

    batch_id TEXT,
    tx_hash TEXT,
    error_message TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_offchain_pending_deposits_status
    ON offchain_pending_deposits(status);

CREATE INDEX IF NOT EXISTS idx_offchain_pending_deposits_batch_id
    ON offchain_pending_deposits(batch_id);

CREATE INDEX IF NOT EXISTS idx_offchain_pending_deposits_owner
    ON offchain_pending_deposits(owner_address);

CREATE TABLE IF NOT EXISTS offchain_pending_withdrawals (
    withdraw_id TEXT PRIMARY KEY,

    owner_address TEXT NOT NULL,
    denom TEXT NOT NULL,
    amount TEXT NOT NULL,
    destination_address TEXT NOT NULL,
    nonce TEXT NOT NULL,
    signature TEXT NOT NULL,
    nullifier TEXT NOT NULL,
    destination_hash TEXT NOT NULL,

    root_before TEXT NOT NULL,
    root_after TEXT NOT NULL,
    balance_before TEXT NOT NULL,
    balance_after TEXT NOT NULL,

    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'included', 'committed', 'failed', 'claimed')),

    batch_id TEXT,
    tx_hash TEXT,
    error_message TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_offchain_pending_withdrawals_nullifier
    ON offchain_pending_withdrawals(nullifier);

CREATE INDEX IF NOT EXISTS idx_offchain_pending_withdrawals_status
    ON offchain_pending_withdrawals(status);

CREATE INDEX IF NOT EXISTS idx_offchain_pending_withdrawals_batch_id
    ON offchain_pending_withdrawals(batch_id);

CREATE INDEX IF NOT EXISTS idx_offchain_pending_withdrawals_owner
    ON offchain_pending_withdrawals(owner_address);