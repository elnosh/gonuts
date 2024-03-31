package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/elnosh/gonuts/cashurpc"
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

	boltdb := &BoltDB{bolt: db}
	err = boltdb.initWalletBuckets()
	if err != nil {
		return nil, fmt.Errorf("error setting bolt db: %v", err)
	}

	return boltdb, nil
}

func (db *BoltDB) initWalletBuckets() error {
	return db.bolt.Update(func(tx *bolt.Tx) error {
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

// return all proofs from db
func (db *BoltDB) GetProofs() []*cashurpc.Proof {
	proofs := make([]*cashurpc.Proof, 0)

	db.bolt.View(func(tx *bolt.Tx) error {
		proofsb := tx.Bucket([]byte(proofsBucket))

		c := proofsb.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var proof *cashurpc.Proof
			if err := json.Unmarshal(v, &proof); err != nil {
				proofs = make([]*cashurpc.Proof, 0)
				return nil
			}
			proofs = append(proofs, proof)
		}
		return nil
	})
	return proofs
}

func (db *BoltDB) GetProofsByKeysetId(id string) []*cashurpc.Proof {
	proofs := make([]*cashurpc.Proof, 0)

	if err := db.bolt.View(func(tx *bolt.Tx) error {
		proofsb := tx.Bucket([]byte(proofsBucket))

		c := proofsb.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var proof *cashurpc.Proof
			if err := json.Unmarshal(v, &proof); err != nil {
				return fmt.Errorf("error getting proofs: %v", err)
			}

			if proof.Id == id {
				proofs = append(proofs, proof)
			}
		}
		return nil
	}); err != nil {
		return make([]*cashurpc.Proof, 0)
	}

	return proofs
}

func (db *BoltDB) SaveProof(proof *cashurpc.Proof) error {
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

func (db *BoltDB) SaveKeyset(keyset *crypto.Keyset) error {
	jsonKeyset, err := json.Marshal(keyset)
	if err != nil {
		return fmt.Errorf("invalid keyset format: %v", err)
	}

	if err := db.bolt.Update(func(tx *bolt.Tx) error {
		keysetsb := tx.Bucket([]byte(keysetsBucket))
		mintBucket, err := keysetsb.CreateBucketIfNotExists([]byte(keyset.MintURL))
		if err != nil {
			return err
		}
		return mintBucket.Put([]byte(keyset.Id), jsonKeyset)
	}); err != nil {
		return fmt.Errorf("error saving keyset: %v", err)
	}
	return nil
}

func (db *BoltDB) GetKeysets() crypto.KeysetsMap {
	keysets := make(crypto.KeysetsMap)

	if err := db.bolt.View(func(tx *bolt.Tx) error {
		keysetsb := tx.Bucket([]byte(keysetsBucket))

		return keysetsb.ForEach(func(mintURL, v []byte) error {
			mintKeysets := make(map[string]crypto.Keyset)

			mintBucket := keysetsb.Bucket(mintURL)
			c := mintBucket.Cursor()

			for k, v := c.First(); k != nil; k, v = c.Next() {
				var keyset crypto.Keyset
				if err := json.Unmarshal(v, &keyset); err != nil {
					return err
				}

				mintKeysets[string(k)] = keyset
			}

			keysets[string(mintURL)] = mintKeysets
			return nil
		})
	}); err != nil {
		return nil
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
		return invoicesb.Put(key, jsonbytes)
	}); err != nil {
		return fmt.Errorf("error saving invoice!: %v", err)
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
