package storage

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	bolt "go.etcd.io/bbolt"
)

const (
	keysetsBucket = "keysets"
	proofsBucket  = "proofs"
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

		return nil
	})
}
