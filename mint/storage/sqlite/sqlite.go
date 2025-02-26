package sqlite

import (
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/storage"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations
var migrations embed.FS

type SQLiteDB struct {
	db *sql.DB
}

// create a temporary directory with the migration files.
// migration files are embedded with go:embed. These are then read
// and copied to a temporary directory.
// This is needed to pass the directory to migrate.New
func migrationsDir() (string, error) {
	tempDir, err := os.MkdirTemp("", "migrations")
	if err != nil {
		return "", err
	}

	migrationFiles, err := migrations.ReadDir("migrations")
	if err != nil {
		return "", err
	}

	for _, file := range migrationFiles {
		filePath := filepath.Join(tempDir, file.Name())

		migrationFilePath := filepath.Join("migrations", file.Name())
		migrationFile, err := migrations.Open(migrationFilePath)
		if err != nil {
			return "", err
		}
		defer migrationFile.Close()

		destFile, err := os.Create(filePath)
		if err != nil {
			return "", err
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, migrationFile)
		if err != nil {
			return "", err
		}
	}

	return tempDir, nil
}

func InitSQLite(path string) (*SQLiteDB, error) {
	dbpath := filepath.Join(path, "mint.sqlite.db")
	db, err := sql.Open("sqlite3", dbpath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	tempMigrationsDir, err := migrationsDir()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempMigrationsDir)

	m, err := migrate.New(fmt.Sprintf("file://%s", tempMigrationsDir), fmt.Sprintf("sqlite3://%s", dbpath))
	if err != nil {
		return nil, err
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return &SQLiteDB{db: db}, nil
}

func (sqlite *SQLiteDB) Close() error {
	return sqlite.db.Close()
}

func (sqlite *SQLiteDB) SaveSeed(seed []byte) error {
	hexSeed := hex.EncodeToString(seed)

	_, err := sqlite.db.Exec(`
	INSERT INTO seed (id, seed) VALUES (?, ?)
	`, "id", hexSeed)

	return err
}

func (sqlite *SQLiteDB) GetSeed() ([]byte, error) {
	var hexSeed string
	row := sqlite.db.QueryRow("SELECT seed FROM seed WHERE id = id")
	err := row.Scan(&hexSeed)
	if err != nil {
		return nil, err
	}

	seed, err := hex.DecodeString(hexSeed)
	if err != nil {
		return nil, err
	}

	return seed, nil
}

func (sqlite *SQLiteDB) SaveKeyset(keyset storage.DBKeyset) error {
	_, err := sqlite.db.Exec(`
		INSERT INTO keysets (id, unit, active, seed, derivation_path_idx, input_fee_ppk) VALUES (?, ?, ?, ?, ?, ?)
	`, keyset.Id, keyset.Unit, keyset.Active, keyset.Seed, keyset.DerivationPathIdx, keyset.InputFeePpk)

	return err
}

func (sqlite *SQLiteDB) GetKeysets() ([]storage.DBKeyset, error) {
	keysets := []storage.DBKeyset{}

	rows, err := sqlite.db.Query("SELECT * FROM keysets")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var keyset storage.DBKeyset
		err := rows.Scan(
			&keyset.Id,
			&keyset.Unit,
			&keyset.Active,
			&keyset.Seed,
			&keyset.DerivationPathIdx,
			&keyset.InputFeePpk,
		)
		if err != nil {
			return nil, err
		}
		keysets = append(keysets, keyset)
	}

	return keysets, nil
}

func (sqlite *SQLiteDB) UpdateKeysetActive(id string, active bool) error {
	result, err := sqlite.db.Exec("UPDATE keysets SET active = ? WHERE id = ?", active, id)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("keyset was not updated")
	}
	return nil
}

func (sqlite *SQLiteDB) SaveProofs(proofs cashu.Proofs) error {
	tx, err := sqlite.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT INTO proofs (y, amount, keyset_id, secret, c, witness) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, proof := range proofs {
		Y, err := crypto.HashToCurve([]byte(proof.Secret))
		if err != nil {
			return err
		}
		Yhex := hex.EncodeToString(Y.SerializeCompressed())

		if _, err := stmt.Exec(Yhex, proof.Amount, proof.Id, proof.Secret, proof.C, proof.Witness); err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

func (sqlite *SQLiteDB) GetProofsUsed(Ys []string) ([]storage.DBProof, error) {
	proofs := []storage.DBProof{}
	query := `SELECT * FROM proofs WHERE y in (?` + strings.Repeat(",?", len(Ys)-1) + `)`

	args := make([]any, len(Ys))
	for i, y := range Ys {
		args[i] = y
	}

	rows, err := sqlite.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var proof storage.DBProof
		var witness sql.NullString

		err := rows.Scan(
			&proof.Y,
			&proof.Amount,
			&proof.Id,
			&proof.Secret,
			&proof.C,
			&witness,
		)
		if err != nil {
			return nil, err
		}
		if witness.Valid {
			proof.Witness = witness.String
		}

		proofs = append(proofs, proof)
	}

	return proofs, nil
}

func (sqlite *SQLiteDB) AddPendingProofs(proofs cashu.Proofs, quoteId string) error {
	tx, err := sqlite.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT INTO pending_proofs (y, amount, keyset_id, secret, c, witness, melt_quote_id) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, proof := range proofs {
		Y, err := crypto.HashToCurve([]byte(proof.Secret))
		if err != nil {
			return err
		}
		Yhex := hex.EncodeToString(Y.SerializeCompressed())

		if _, err := stmt.Exec(Yhex, proof.Amount, proof.Id, proof.Secret, proof.C, proof.Witness, quoteId); err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

func (sqlite *SQLiteDB) GetPendingProofs(Ys []string) ([]storage.DBProof, error) {
	proofs := []storage.DBProof{}
	query := `SELECT * FROM pending_proofs WHERE y in (?` + strings.Repeat(",?", len(Ys)-1) + `)`

	args := make([]any, len(Ys))
	for i, y := range Ys {
		args[i] = y
	}

	rows, err := sqlite.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var proof storage.DBProof
		var witness sql.NullString

		err := rows.Scan(
			&proof.Y,
			&proof.Amount,
			&proof.Id,
			&proof.Secret,
			&proof.C,
			&proof.MeltQuoteId,
			&witness,
		)
		if err != nil {
			return nil, err
		}

		if witness.Valid {
			proof.Witness = witness.String
		}

		proofs = append(proofs, proof)
	}

	return proofs, nil
}

func (sqlite *SQLiteDB) GetPendingProofsByQuote(quoteId string) ([]storage.DBProof, error) {
	proofs := []storage.DBProof{}
	query := `SELECT y, amount, keyset_id, secret, c, witness FROM pending_proofs WHERE melt_quote_id = ?`

	rows, err := sqlite.db.Query(query, quoteId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var proof storage.DBProof
		var witness sql.NullString

		err := rows.Scan(
			&proof.Y,
			&proof.Amount,
			&proof.Id,
			&proof.Secret,
			&proof.C,
			&witness,
		)
		if err != nil {
			return nil, err
		}

		if witness.Valid {
			proof.Witness = witness.String
		}

		proofs = append(proofs, proof)
	}

	return proofs, nil
}

func (sqlite *SQLiteDB) RemovePendingProofs(Ys []string) error {
	tx, err := sqlite.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare("DELETE FROM pending_proofs WHERE y = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, y := range Ys {
		if _, err := stmt.Exec(y); err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

func (sqlite *SQLiteDB) SaveMintQuote(mintQuote storage.MintQuote) error {
	var pubkey string
	if mintQuote.Pubkey != nil {
		pubkey = hex.EncodeToString(mintQuote.Pubkey.SerializeCompressed())
	}

	_, err := sqlite.db.Exec(
		`INSERT INTO mint_quotes (id, payment_request, payment_hash, amount, state, expiry, pubkey)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		mintQuote.Id,
		mintQuote.PaymentRequest,
		mintQuote.PaymentHash,
		mintQuote.Amount,
		mintQuote.State.String(),
		mintQuote.Expiry,
		pubkey,
	)

	return err
}

func (sqlite *SQLiteDB) GetMintQuote(quoteId string) (storage.MintQuote, error) {
	row := sqlite.db.QueryRow("SELECT * FROM mint_quotes WHERE id = ?", quoteId)

	var mintQuote storage.MintQuote
	var state string
	var pubkey sql.NullString

	err := row.Scan(
		&mintQuote.Id,
		&mintQuote.PaymentRequest,
		&mintQuote.PaymentHash,
		&mintQuote.Amount,
		&state,
		&mintQuote.Expiry,
		&pubkey,
	)
	if err != nil {
		return storage.MintQuote{}, err
	}
	mintQuote.State = nut04.StringToState(state)

	if pubkey.Valid && len(pubkey.String) > 0 {
		// these should not error because validation is done before saving with public key
		// if there is an error, something bad happened
		hexPubkey, err := hex.DecodeString(pubkey.String)
		if err != nil {
			return storage.MintQuote{}, fmt.Errorf("invalid public key in db: %v", err)
		}

		publicKey, err := secp256k1.ParsePubKey(hexPubkey)
		if err != nil {
			return storage.MintQuote{}, fmt.Errorf("invalid public key in db: %v", err)
		}
		mintQuote.Pubkey = publicKey
	}

	return mintQuote, nil
}

func (sqlite *SQLiteDB) GetMintQuoteByPaymentHash(paymentHash string) (storage.MintQuote, error) {
	row := sqlite.db.QueryRow("SELECT * FROM mint_quotes WHERE payment_hash = ?", paymentHash)

	var mintQuote storage.MintQuote
	var state string
	var pubkey sql.NullString

	err := row.Scan(
		&mintQuote.Id,
		&mintQuote.PaymentRequest,
		&mintQuote.PaymentHash,
		&mintQuote.Amount,
		&state,
		&mintQuote.Expiry,
		&pubkey,
	)
	if err != nil {
		return storage.MintQuote{}, err
	}
	mintQuote.State = nut04.StringToState(state)

	if pubkey.Valid && len(pubkey.String) > 0 {
		// these should not error because validation is done before saving with public key
		// if there is an error, something bad happened
		hexPubkey, err := hex.DecodeString(pubkey.String)
		if err != nil {
			return storage.MintQuote{}, fmt.Errorf("invalid public key in db: %v", err)
		}

		publicKey, err := secp256k1.ParsePubKey(hexPubkey)
		if err != nil {
			return storage.MintQuote{}, fmt.Errorf("invalid public key in db: %v", err)
		}
		mintQuote.Pubkey = publicKey
	}

	return mintQuote, nil
}

func (sqlite *SQLiteDB) UpdateMintQuoteState(quoteId string, state nut04.State) error {
	updatedState := state.String()
	result, err := sqlite.db.Exec("UPDATE mint_quotes SET state = ? WHERE id = ?", updatedState, quoteId)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("mint quote was not updated")
	}
	return nil
}

func (sqlite *SQLiteDB) SaveMeltQuote(meltQuote storage.MeltQuote) error {
	_, err := sqlite.db.Exec(`
		INSERT INTO melt_quotes 
		(id, request, payment_hash, amount, fee_reserve, state, expiry, preimage, is_mpp, amount_msat) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		meltQuote.Id,
		meltQuote.InvoiceRequest,
		meltQuote.PaymentHash,
		meltQuote.Amount,
		meltQuote.FeeReserve,
		meltQuote.State.String(),
		meltQuote.Expiry,
		meltQuote.Preimage,
		meltQuote.IsMpp,
		meltQuote.AmountMsat,
	)

	return err
}

func (sqlite *SQLiteDB) GetMeltQuote(quoteId string) (storage.MeltQuote, error) {
	row := sqlite.db.QueryRow("SELECT * FROM melt_quotes WHERE id = ?", quoteId)

	var meltQuote storage.MeltQuote
	var state string
	var isMpp sql.NullBool
	var amountMsat sql.NullInt64

	err := row.Scan(
		&meltQuote.Id,
		&meltQuote.InvoiceRequest,
		&meltQuote.PaymentHash,
		&meltQuote.Amount,
		&meltQuote.FeeReserve,
		&state,
		&meltQuote.Expiry,
		&meltQuote.Preimage,
		&isMpp,
		&amountMsat,
	)
	if err != nil {
		return storage.MeltQuote{}, err
	}
	meltQuote.State = nut05.StringToState(state)
	if isMpp.Valid {
		meltQuote.IsMpp = isMpp.Bool
	}
	if amountMsat.Valid {
		meltQuote.AmountMsat = uint64(amountMsat.Int64)
	}

	return meltQuote, nil
}

func (sqlite *SQLiteDB) GetMeltQuoteByPaymentRequest(invoice string) (*storage.MeltQuote, error) {
	row := sqlite.db.QueryRow("SELECT * FROM melt_quotes WHERE request = ?", invoice)

	var meltQuote storage.MeltQuote
	var state string
	var isMpp sql.NullBool
	var amountMsat sql.NullInt64

	err := row.Scan(
		&meltQuote.Id,
		&meltQuote.InvoiceRequest,
		&meltQuote.PaymentHash,
		&meltQuote.Amount,
		&meltQuote.FeeReserve,
		&state,
		&meltQuote.Expiry,
		&meltQuote.Preimage,
		&isMpp,
		&amountMsat,
	)
	if err != nil {
		return nil, err
	}
	meltQuote.State = nut05.StringToState(state)
	if isMpp.Valid {
		meltQuote.IsMpp = isMpp.Bool
	}
	if amountMsat.Valid {
		meltQuote.AmountMsat = uint64(amountMsat.Int64)
	}

	return &meltQuote, nil
}

func (sqlite *SQLiteDB) UpdateMeltQuote(quoteId, preimage string, state nut05.State) error {
	updatedState := state.String()
	result, err := sqlite.db.Exec(
		"UPDATE melt_quotes SET state = ?, preimage = ? WHERE id = ?",
		updatedState, preimage, quoteId,
	)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("melt quote was not updated")
	}
	return nil
}

func (sqlite *SQLiteDB) SaveBlindSignatures(B_s []string, blindSignatures cashu.BlindedSignatures) error {
	tx, err := sqlite.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT INTO blind_signatures (b_, c_, keyset_id, amount, e, s) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, sig := range blindSignatures {
		if _, err := stmt.Exec(B_s[i], sig.C_, sig.Id, sig.Amount, sig.DLEQ.E, sig.DLEQ.S); err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

func (sqlite *SQLiteDB) GetBlindSignature(B_ string) (cashu.BlindedSignature, error) {
	row := sqlite.db.QueryRow("SELECT amount, c_, keyset_id, e, s FROM blind_signatures WHERE b_ = ?", B_)

	var signature cashu.BlindedSignature
	var e sql.NullString
	var s sql.NullString

	err := row.Scan(
		&signature.Amount,
		&signature.C_,
		&signature.Id,
		&e,
		&s,
	)
	if err != nil {
		return cashu.BlindedSignature{}, err
	}

	if !e.Valid || !s.Valid {
		signature.DLEQ = nil
	} else {
		signature.DLEQ = &cashu.DLEQProof{
			E: e.String,
			S: s.String,
		}
	}

	return signature, nil
}

func (sqlite *SQLiteDB) GetBlindSignatures(B_s []string) (cashu.BlindedSignatures, error) {
	signatures := cashu.BlindedSignatures{}
	query := `SELECT amount, c_, keyset_id, e, s FROM blind_signatures WHERE b_ in (?` + strings.Repeat(",?", len(B_s)-1) + `)`

	args := make([]any, len(B_s))
	for i, B_ := range B_s {
		args[i] = B_
	}

	rows, err := sqlite.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var signature cashu.BlindedSignature
		var e sql.NullString
		var s sql.NullString

		err := rows.Scan(
			&signature.Amount,
			&signature.C_,
			&signature.Id,
			&e,
			&s,
		)
		if err != nil {
			return nil, err
		}

		if !e.Valid || !s.Valid {
			signature.DLEQ = nil
		} else {
			signature.DLEQ = &cashu.DLEQProof{
				E: e.String,
				S: s.String,
			}
		}

		signatures = append(signatures, signature)
	}

	return signatures, nil
}

func (sqlite *SQLiteDB) GetEcashIssued() (map[string]uint64, error) {
	ecashIssued := make(map[string]uint64)

	rows, err := sqlite.db.Query("SELECT * FROM total_issued")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var keysetId string
		var amount uint64
		if err := rows.Scan(&keysetId, &amount); err != nil {
			return nil, err
		}
		ecashIssued[keysetId] = amount
	}

	return ecashIssued, nil
}

func (sqlite *SQLiteDB) GetEcashRedeemed() (map[string]uint64, error) {
	ecashRedeemed := make(map[string]uint64)

	rows, err := sqlite.db.Query("SELECT * FROM total_redeemed")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var keysetId string
		var amount uint64
		if err := rows.Scan(&keysetId, &amount); err != nil {
			return nil, err
		}
		ecashRedeemed[keysetId] = amount
	}

	return ecashRedeemed, nil
}
