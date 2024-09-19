package sqlite

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

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

type SQLiteDB struct {
	db *sql.DB
}

func InitSQLite(path, migrationPath string) (*SQLiteDB, error) {
	dbpath := filepath.Join(path, "mint.sqlite.db")
	db, err := sql.Open("sqlite3", dbpath)
	if err != nil {
		return nil, err
	}

	m, err := migrate.New(fmt.Sprintf("file://%s", migrationPath), fmt.Sprintf("sqlite3://%s", dbpath))
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

func (sqlite *SQLiteDB) Close() {
	sqlite.db.Close()
}

func (sqlite *SQLiteDB) GetBalance() (uint64, error) {
	var balance uint64
	row := sqlite.db.QueryRow("SELECT balance FROM balance")
	err := row.Scan(&balance)
	if err != nil {
		return 0, err
	}
	return balance, nil
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

	stmt, err := tx.Prepare("INSERT INTO proofs (y, amount, keyset_id, secret, c) VALUES (?, ?, ?, ?, ?)")
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

		if _, err := stmt.Exec(Yhex, proof.Amount, proof.Id, proof.Secret, proof.C); err != nil {
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
		err := rows.Scan(
			&proof.Y,
			&proof.Amount,
			&proof.Id,
			&proof.Secret,
			&proof.C,
		)
		if err != nil {
			return nil, err
		}

		proofs = append(proofs, proof)
	}

	return proofs, nil
}

func (sqlite *SQLiteDB) SaveMintQuote(mintQuote storage.MintQuote) error {
	_, err := sqlite.db.Exec(
		`INSERT INTO mint_quotes (id, payment_request, payment_hash, amount, state, expiry) 
		VALUES (?, ?, ?, ?, ?, ?)`,
		mintQuote.Id,
		mintQuote.PaymentRequest,
		mintQuote.PaymentHash,
		mintQuote.Amount,
		mintQuote.State.String(),
		mintQuote.Expiry,
	)

	return err
}

func (sqlite *SQLiteDB) GetMintQuote(quoteId string) (storage.MintQuote, error) {
	row := sqlite.db.QueryRow("SELECT * FROM mint_quotes WHERE id = ?", quoteId)

	var mintQuote storage.MintQuote
	var state string

	err := row.Scan(
		&mintQuote.Id,
		&mintQuote.PaymentRequest,
		&mintQuote.PaymentHash,
		&mintQuote.Amount,
		&state,
		&mintQuote.Expiry,
	)
	if err != nil {
		return storage.MintQuote{}, err
	}
	mintQuote.State = nut04.StringToState(state)

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
		(id, request, payment_hash, amount, fee_reserve, state, expiry, preimage) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		meltQuote.Id,
		meltQuote.InvoiceRequest,
		meltQuote.PaymentHash,
		meltQuote.Amount,
		meltQuote.FeeReserve,
		meltQuote.State.String(),
		meltQuote.Expiry,
		meltQuote.Preimage,
	)

	return err
}

func (sqlite *SQLiteDB) GetMeltQuote(quoteId string) (storage.MeltQuote, error) {
	row := sqlite.db.QueryRow("SELECT * FROM melt_quotes WHERE id = ?", quoteId)

	var meltQuote storage.MeltQuote
	var state string

	err := row.Scan(
		&meltQuote.Id,
		&meltQuote.InvoiceRequest,
		&meltQuote.PaymentHash,
		&meltQuote.Amount,
		&meltQuote.FeeReserve,
		&state,
		&meltQuote.Expiry,
		&meltQuote.Preimage,
	)
	if err != nil {
		return storage.MeltQuote{}, err
	}
	meltQuote.State = nut05.StringToState(state)

	return meltQuote, nil
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

func (sqlite *SQLiteDB) SaveBlindSignature(B_ string, blindSignature cashu.BlindedSignature) error {
	_, err := sqlite.db.Exec(`
		INSERT INTO blind_signatures (b_, c_, keyset_id, amount, e, s) VALUES (?, ?, ?, ?, ?, ?)`,
		B_,
		blindSignature.C_,
		blindSignature.Id,
		blindSignature.Amount,
		blindSignature.DLEQ.E,
		blindSignature.DLEQ.S,
	)
	return err
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
