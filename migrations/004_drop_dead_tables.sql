-- 004_drop_dead_tables.sql
-- INT-DB2: drop two tables defined in 001_init.sql that NO code path ever
-- writes or reads (verified: 0 Go references). They misled the schema (a reader
-- querying the DB found them permanently empty) and are removed for honesty.
--
--   indexed_deposits          — superseded by offchain_pending_deposits, the
--                               durable deposit store actually used in pending
--                               mode. Keeping two deposit tables invited drift, so
--                               it is dropped, NOT wired. (If a durable deposit
--                               list is ever needed, point GET /api/deposits at
--                               offchain_pending_deposits instead of reviving this.)
--   chain_indexer_checkpoints — the deposit poller never persisted a checkpoint;
--                               the chain runs with --reset-once (fresh each run)
--                               and deposits are idempotent (IsDepositApplied), so
--                               resume has no value in this deployment.
--
-- DROP TABLE also removes each table's indexes. No table has a foreign key to
-- these, so no CASCADE is required. IF EXISTS keeps this idempotent so pg_up.sh
-- can re-apply the full migration set (001 re-creates via IF NOT EXISTS, this
-- migration — always last — drops again, ending each run with them absent).

DROP TABLE IF EXISTS indexed_deposits;
DROP TABLE IF EXISTS chain_indexer_checkpoints;
