CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);
CREATE TABLE IF NOT EXISTS problems (
 id INTEGER PRIMARY KEY, kind TEXT NOT NULL, title TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS problem_revisions (
 revision_hash TEXT PRIMARY KEY, problem_id INTEGER NOT NULL REFERENCES problems(id),
 statement_markdown TEXT NOT NULL, public_tests_json TEXT NOT NULL,
 constraints_json TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS execution_contracts (
 contract_hash TEXT PRIMARY KEY, contract_json TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS problem_snapshots (
 revision_hash TEXT NOT NULL REFERENCES problem_revisions(revision_hash),
 contract_hash TEXT NOT NULL REFERENCES execution_contracts(contract_hash),
 snapshot_json TEXT NOT NULL, updated_at TEXT NOT NULL,
 PRIMARY KEY (revision_hash, contract_hash)
);
CREATE TABLE IF NOT EXISTS solutions (
 id INTEGER PRIMARY KEY, revision_hash TEXT NOT NULL, contract_hash TEXT NOT NULL,
 files_hash TEXT NOT NULL, source TEXT NOT NULL, provider TEXT NOT NULL,
 model TEXT NOT NULL, prompt_version TEXT NOT NULL,
 parent_candidate_id INTEGER REFERENCES solutions(id), created_at TEXT NOT NULL,
 FOREIGN KEY (revision_hash,contract_hash) REFERENCES problem_snapshots(revision_hash,contract_hash),
 UNIQUE (revision_hash,contract_hash,files_hash)
);
CREATE TABLE IF NOT EXISTS solution_files (
 solution_id INTEGER NOT NULL REFERENCES solutions(id), path TEXT NOT NULL,
 content TEXT NOT NULL, content_hash TEXT NOT NULL, PRIMARY KEY (solution_id,path)
);
CREATE TABLE IF NOT EXISTS verifications (
 id INTEGER PRIMARY KEY, solution_id INTEGER NOT NULL REFERENCES solutions(id),
 evidence_json TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS solver_runs (
 id TEXT PRIMARY KEY, owner TEXT NOT NULL, idempotency_key TEXT NOT NULL,
 status TEXT NOT NULL, data_json TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS solver_run_idempotency ON solver_runs(owner,idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX IF NOT EXISTS solver_run_owner ON solver_runs(owner,updated_at);
CREATE TABLE IF NOT EXISTS attempts (
 id INTEGER PRIMARY KEY, run_id TEXT NOT NULL REFERENCES solver_runs(id),
 solution_id INTEGER REFERENCES solutions(id), attempt_number INTEGER NOT NULL,
 stage TEXT NOT NULL, result TEXT NOT NULL, feedback TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS usage_ledger (
 id INTEGER PRIMARY KEY, run_id TEXT NOT NULL REFERENCES solver_runs(id),
 solution_id INTEGER REFERENCES solutions(id), provider TEXT NOT NULL,
 model TEXT NOT NULL, pricing_version TEXT NOT NULL, input_tokens INTEGER NOT NULL,
 cached_input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
 reserved_micro_usd INTEGER NOT NULL, cost_micro_usd INTEGER NOT NULL,
 status TEXT NOT NULL, finish_reason TEXT NOT NULL, latency_millis INTEGER NOT NULL,
 created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS usage_run ON usage_ledger(run_id);
INSERT OR IGNORE INTO schema_migrations(version) VALUES(1);
