package mint

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	bolt "go.etcd.io/bbolt"
)

const (
	keysetsBucket  = "keysets"
	invoicesBucket = "invoices"

	// for all redeemed proofs
	proofsBucket = "proofs"

	quotesBucket = "quotes"
)

type BoltDB struct {
	bolt *bolt.DB
}

func InitBolt(path string) (*BoltDB, error) {
	db, err := bolt.Open(filepath.Join(path, "mint.db"), 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("error setting bolt db: %v", err)
	}

	boltdb := &BoltDB{bolt: db}
	err = boltdb.initMintBuckets()
	if err != nil {
		return nil, fmt.Errorf("error setting bolt db: %v", err)
	}

	return boltdb, nil
}

func (db *BoltDB) initMintBuckets() error {
	return db.bolt.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(keysetsBucket))
		if err != nil {
			return err
		}

		_, err = tx.CreateBucketIfNotExists([]byte(invoicesBucket))
		if err != nil {
			return err
		}

		_, err = tx.CreateBucketIfNotExists([]byte(proofsBucket))
		if err != nil {
			return err
		}

		_, err = tx.CreateBucketIfNotExists([]byte(quotesBucket))
		if err != nil {
			return err
		}

		return nil
	})
}

func (db *BoltDB) GetKeysets() map[string]crypto.Keyset {
	keysets := make(map[string]crypto.Keyset)

	db.bolt.View(func(tx *bolt.Tx) error {
		keysetsBucket := tx.Bucket([]byte(keysetsBucket))

		c := keysetsBucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var keyset crypto.Keyset
			if err := json.Unmarshal(v, &keyset); err != nil {
				break
			}
			keysets[string(k)] = keyset
		}
		return nil
	})

	return keysets
}

func (db *BoltDB) SaveKeyset(keyset *crypto.Keyset) error {
	jsonKeyset, err := json.Marshal(keyset)
	if err != nil {
		return fmt.Errorf("invalid keyset: %v", err)
	}

	if err := db.bolt.Update(func(tx *bolt.Tx) error {
		keysetsb := tx.Bucket([]byte(keysetsBucket))
		key := []byte(keyset.Id)
		return keysetsb.Put(key, jsonKeyset)
	}); err != nil {
		return fmt.Errorf("error saving keyset: %v", err)
	}
	return nil
}

type dbproof struct {
	Y      []byte
	Amount uint64 `json:"amount"`
	Id     string `json:"id"`
	Secret string `json:"secret"`
	C      string `json:"C"`
}

func (db *BoltDB) GetProof(secret string) *cashu.Proof {
	var proof *cashu.Proof
	Y := crypto.HashToCurveDeprecated([]byte(secret))

	db.bolt.View(func(tx *bolt.Tx) error {
		proofsb := tx.Bucket([]byte(proofsBucket))
		proofBytes := proofsb.Get(Y.SerializeCompressed())
		err := json.Unmarshal(proofBytes, &proof)
		if err != nil {
			proof = nil
		}
		return nil
	})
	return proof
}

func (db *BoltDB) SaveProof(proof cashu.Proof) error {
	Y := crypto.HashToCurveDeprecated([]byte(proof.Secret))

	dbproof := dbproof{
		Y:      Y.SerializeCompressed(),
		Amount: proof.Amount,
		Id:     proof.Id,
		Secret: proof.Secret,
		C:      proof.C,
	}
	jsonProof, err := json.Marshal(dbproof)
	if err != nil {
		return fmt.Errorf("invalid proof format: %v", err)
	}

	if err := db.bolt.Update(func(tx *bolt.Tx) error {
		proofsb := tx.Bucket([]byte(proofsBucket))
		return proofsb.Put(Y.SerializeCompressed(), jsonProof)
	}); err != nil {
		return fmt.Errorf("error saving proof: %v", err)
	}
	return nil
}

func (db *BoltDB) SaveInvoice(invoice lightning.Invoice) error {
	jsonbytes, err := json.Marshal(invoice)
	if err != nil {
		return fmt.Errorf("invalid invoice: %v", err)
	}

	if err := db.bolt.Update(func(tx *bolt.Tx) error {
		invoicesb := tx.Bucket([]byte(invoicesBucket))
		key := []byte(invoice.Id)
		err := invoicesb.Put(key, jsonbytes)
		return err
	}); err != nil {
		return fmt.Errorf("error saving invoice: %v", err)
	}
	return nil
}

func (db *BoltDB) GetInvoice(id string) *lightning.Invoice {
	var invoice *lightning.Invoice

	db.bolt.View(func(tx *bolt.Tx) error {
		invoicesb := tx.Bucket([]byte(invoicesBucket))
		invoiceBytes := invoicesb.Get([]byte(id))
		err := json.Unmarshal(invoiceBytes, &invoice)
		if err != nil {
			invoice = nil
		}

		return nil
	})
	return invoice
}

func (db *BoltDB) SaveMeltQuote(quote MeltQuote) error {
	jsonbytes, err := json.Marshal(quote)
	if err != nil {
		return fmt.Errorf("invalid quote: %v", err)
	}

	if err := db.bolt.Update(func(tx *bolt.Tx) error {
		meltQuotesb := tx.Bucket([]byte(quotesBucket))
		key := []byte(quote.Id)
		err := meltQuotesb.Put(key, jsonbytes)
		return err
	}); err != nil {
		return fmt.Errorf("error saving quote: %v", err)
	}
	return nil
}

func (db *BoltDB) GetMeltQuote(quoteId string) *MeltQuote {
	var quote *MeltQuote

	db.bolt.View(func(tx *bolt.Tx) error {
		meltQuotesb := tx.Bucket([]byte(quotesBucket))
		quoteBytes := meltQuotesb.Get([]byte(quoteId))
		err := json.Unmarshal(quoteBytes, &quote)
		if err != nil {
			quote = nil
		}

		return nil
	})
	return quote
}
