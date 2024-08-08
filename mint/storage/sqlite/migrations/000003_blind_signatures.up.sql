
CREATE TABLE IF NOT EXISTS blind_signatures (
	b_ TEXT NOT NULL PRIMARY KEY,
	c_ TEXT NOT NULL,
	keyset_id TEXT NOT NULL,
	amount INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_blind_signatures_b ON blind_signatures(b_);
