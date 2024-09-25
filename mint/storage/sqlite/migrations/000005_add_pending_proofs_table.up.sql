CREATE TABLE IF NOT EXISTS pending_proofs (
	y TEXT PRIMARY KEY,
	amount INTEGER NOT NULL,
	keyset_id TEXT NOT NULL,
	secret TEXT NOT NULL UNIQUE,
	c TEXT NOT NULL,
	melt_quote_id TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pending_proofs_y ON pending_proofs(y);
