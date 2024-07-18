
CREATE TABLE IF NOT EXISTS seed (
	id TEXT NOT NULL PRIMARY KEY,
	seed TEXT
);

CREATE TABLE IF NOT EXISTS keysets (
	id TEXT NOT NULL PRIMARY KEY,
	unit TEXT NOT NULL,
	active BOOLEAN NOT NULL,
	seed TEXT NOT NULL,
	derivation_path_idx INTEGER NOT NULL,
	input_fee_ppk INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS proofs (
	y TEXT PRIMARY KEY,
	amount INTEGER NOT NULL,
	keyset_id TEXT NOT NULL,
	secret TEXT NOT NULL UNIQUE,
	c TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_proofs_y ON proofs(y);

CREATE TABLE IF NOT EXISTS mint_quotes (
	id TEXT PRIMARY KEY,
	payment_request TEXT NOT NULL,
	payment_hash TEXT,
	amount INTEGER NOT NULL,
	state TEXT NOT NULL,
	expiry INTEGER 
);

CREATE INDEX IF NOT EXISTS idx_mint_quotes_id ON mint_quotes(id);

CREATE TABLE IF NOT EXISTS melt_quotes (
	id TEXT NOT NULL PRIMARY KEY,
	request TEXT NOT NULL,
	payment_hash TEXT,
	amount INTEGER NOT NULL,
	fee_reserve INTEGER NOT NULL,
	state TEXT NOT NULL,
	expiry INTEGER, 
	preimage TEXT
);

CREATE INDEX IF NOT EXISTS idx_melt_quotes_id ON melt_quotes(id);

