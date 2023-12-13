package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
	bolt "go.etcd.io/bbolt"
)

const (
	keysetsBucket  = "keysets"
	proofsBucket   = "proofs"
	invoicesBucket = "invoices"
)

type BoltDB struct {
	bolt *bolt.DB
}

func InitBolt(path string) (*BoltDB, error) {
	db, err := bolt.Open(filepath.Join(path, "wallet.db"), 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("error setting bolt db: %v", err)
	}

	err = initWalletBuckets(db)
	if err != nil {
		return nil, fmt.Errorf("error setting bolt db: %v", err)
	}

	return &BoltDB{bolt: db}, nil
}

func (db *BoltDB) GetProofs() cashu.Proofs {
	proofs := cashu.Proofs{}

	if err := db.bolt.View(func(tx *bolt.Tx) error {
		proofsb := tx.Bucket([]byte(proofsBucket))

		c := proofsb.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var proof cashu.Proof
			if err := json.Unmarshal(v, &proof); err != nil {
				return fmt.Errorf("error getting proofs: %v", err)
			}
			proofs = append(proofs, proof)
		}
		return nil
	}); err != nil {
		return cashu.Proofs{}
	}

	return proofs
}

func (db *BoltDB) SaveProof(proof cashu.Proof) error {
	jsonProof, err := json.Marshal(proof)
	if err != nil {
		return fmt.Errorf("invalid proof format: %v", err)
	}

	if err := db.bolt.Update(func(tx *bolt.Tx) error {
		proofsb := tx.Bucket([]byte(proofsBucket))
		key := []byte(proof.Secret)
		return proofsb.Put(key, jsonProof)
	}); err != nil {
		return fmt.Errorf("error saving proof: %v", err)
	}
	return nil
}

func (db *BoltDB) DeleteProof(secret string) error {
	return db.bolt.Update(func(tx *bolt.Tx) error {
		proofsb := tx.Bucket([]byte(proofsBucket))
		val := proofsb.Get([]byte(secret))
		if val == nil {
			return errors.New("proof does not exist")
		}

		return proofsb.Delete([]byte(secret))
	})
}

func (db *BoltDB) GetKeysets() []crypto.Keyset {
	keysets := []crypto.Keyset{}

	if err := db.bolt.View(func(tx *bolt.Tx) error {
		keysetsb := tx.Bucket([]byte(keysetsBucket))

		c := keysetsb.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var keyset crypto.Keyset
			if err := json.Unmarshal(v, &keyset); err != nil {
				return fmt.Errorf("error getting proofs: %v", err)
			}
			keysets = append(keysets, keyset)
		}
		return nil
	}); err != nil {
		return []crypto.Keyset{}
	}

	return keysets
}

func (db *BoltDB) SaveInvoice(invoice lightning.Invoice) error {
	jsonbytes, err := json.Marshal(invoice)
	if err != nil {
		return fmt.Errorf("invalid invoice: %v", err)
	}

	if err := db.bolt.Update(func(tx *bolt.Tx) error {
		invoicesb := tx.Bucket([]byte(invoicesBucket))
		key := []byte(invoice.PaymentRequest)
		err := invoicesb.Put(key, jsonbytes)
		return err
	}); err != nil {
		return fmt.Errorf("error saving invoice: %v", err)
	}
	return nil
}

func (db *BoltDB) GetInvoice(pr string) *lightning.Invoice {
	var invoice *lightning.Invoice

	db.bolt.View(func(tx *bolt.Tx) error {
		invoicesb := tx.Bucket([]byte(invoicesBucket))
		invoiceBytes := invoicesb.Get([]byte(pr))
		err := json.Unmarshal(invoiceBytes, &invoice)
		if err != nil {
			invoice = nil
		}

		return nil
	})
	return invoice
}

func initWalletBuckets(db *bolt.DB) error {
	return db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(keysetsBucket))
		if err != nil {
			return err
		}

		_, err = tx.CreateBucketIfNotExists([]byte(proofsBucket))
		if err != nil {
			return err
		}

		_, err = tx.CreateBucketIfNotExists([]byte(invoicesBucket))
		if err != nil {
			return err
		}

		return nil
	})
}
