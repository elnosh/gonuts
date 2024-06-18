package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	bolt "go.etcd.io/bbolt"
)

const (
	keysetsBucket  = "keysets"
	proofsBucket   = "proofs"
	invoicesBucket = "invoices"
	seedBucket     = "seed"
	mnemonicKey    = "mnemonic"
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

		_, err = tx.CreateBucketIfNotExists([]byte(seedBucket))
		if err != nil {
			return err
		}

		return nil
	})
}

func (db *BoltDB) SaveMnemonicSeed(mnemonic string, seed []byte) {
	db.bolt.Update(func(tx *bolt.Tx) error {
		seedb := tx.Bucket([]byte(seedBucket))
		seedb.Put([]byte(seedBucket), seed)
		seedb.Put([]byte(mnemonicKey), []byte(mnemonic))
		return nil
	})
}

func (db *BoltDB) GetMnemonic() string {
	var mnemonic string
	db.bolt.View(func(tx *bolt.Tx) error {
		seedb := tx.Bucket([]byte(seedBucket))
		mnemonic = string(seedb.Get([]byte(mnemonicKey)))
		return nil
	})
	return mnemonic
}

func (db *BoltDB) GetSeed() []byte {
	var seed []byte
	db.bolt.View(func(tx *bolt.Tx) error {
		seedb := tx.Bucket([]byte(seedBucket))
		seed = seedb.Get([]byte(seedBucket))
		return nil
	})
	return seed
}

// return all proofs from db
func (db *BoltDB) GetProofs() cashu.Proofs {
	proofs := cashu.Proofs{}

	db.bolt.View(func(tx *bolt.Tx) error {
		proofsb := tx.Bucket([]byte(proofsBucket))

		c := proofsb.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var proof cashu.Proof
			if err := json.Unmarshal(v, &proof); err != nil {
				proofs = cashu.Proofs{}
				return nil
			}
			proofs = append(proofs, proof)
		}
		return nil
	})
	return proofs
}

func (db *BoltDB) GetProofsByKeysetId(id string) cashu.Proofs {
	proofs := cashu.Proofs{}

	if err := db.bolt.View(func(tx *bolt.Tx) error {
		proofsb := tx.Bucket([]byte(proofsBucket))

		c := proofsb.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var proof cashu.Proof
			if err := json.Unmarshal(v, &proof); err != nil {
				return fmt.Errorf("error getting proofs: %v", err)
			}

			if proof.Id == id {
				proofs = append(proofs, proof)
			}
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

func (db *BoltDB) SaveKeyset(keyset *crypto.WalletKeyset) error {
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
			mintKeysets := make(map[string]crypto.WalletKeyset)
			mintBucket := keysetsb.Bucket(mintURL)
			c := mintBucket.Cursor()

			for k, v := c.First(); k != nil; k, v = c.Next() {
				var keyset crypto.WalletKeyset
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

func (db *BoltDB) GetKeyset(keysetId string) *crypto.WalletKeyset {
	var keyset *crypto.WalletKeyset

	db.bolt.View(func(tx *bolt.Tx) error {
		keysetsb := tx.Bucket([]byte(keysetsBucket))

		return keysetsb.ForEach(func(mintURL, v []byte) error {
			mintBucket := keysetsb.Bucket(mintURL)
			keysetBytes := mintBucket.Get([]byte(keysetId))
			if keysetBytes != nil {
				err := json.Unmarshal(keysetBytes, &keyset)
				if err != nil {
					return err
				}
			}
			return nil
		})
	})

	return keyset
}

func (db *BoltDB) IncrementKeysetCounter(keysetId string, num uint32) error {
	if err := db.bolt.Update(func(tx *bolt.Tx) error {
		keysetsb := tx.Bucket([]byte(keysetsBucket))
		var keyset *crypto.WalletKeyset
		keysetFound := false

		err := keysetsb.ForEach(func(mintURL, v []byte) error {
			mintBucket := keysetsb.Bucket(mintURL)

			keysetBytes := mintBucket.Get([]byte(keysetId))
			if keysetBytes != nil {
				err := json.Unmarshal(keysetBytes, &keyset)
				if err != nil {
					return fmt.Errorf("error reading keyset from db: %v", err)
				}
				keyset.Counter += num

				jsonBytes, err := json.Marshal(keyset)
				if err != nil {
					return err
				}
				keysetFound = true
				return mintBucket.Put([]byte(keysetId), jsonBytes)
			}

			return nil
		})

		if !keysetFound {
			return errors.New("keyset does not exist")
		}

		return err
	}); err != nil {
		return err
	}

	return nil
}

func (db *BoltDB) GetKeysetCounter(keysetId string) uint32 {
	var counter uint32 = 0

	if err := db.bolt.Update(func(tx *bolt.Tx) error {
		keysetsb := tx.Bucket([]byte(keysetsBucket))
		var keyset *crypto.WalletKeyset
		keysetFound := false

		err := keysetsb.ForEach(func(mintURL, v []byte) error {
			mintBucket := keysetsb.Bucket(mintURL)

			keysetBytes := mintBucket.Get([]byte(keysetId))
			if keysetBytes != nil {
				err := json.Unmarshal(keysetBytes, &keyset)
				if err != nil {
					return err
				}
				counter = keyset.Counter
				keysetFound = true
				return nil
			}
			return nil
		})

		if !keysetFound {
			return errors.New("keyset does not exist")
		}

		return err
	}); err != nil {
		return 0
	}

	return counter
}

func (db *BoltDB) SaveInvoice(invoice Invoice) error {
	jsonbytes, err := json.Marshal(invoice)
	if err != nil {
		return fmt.Errorf("invalid invoice: %v", err)
	}

	if err := db.bolt.Update(func(tx *bolt.Tx) error {
		invoicesb := tx.Bucket([]byte(invoicesBucket))
		key := []byte(invoice.PaymentHash)
		return invoicesb.Put(key, jsonbytes)
	}); err != nil {
		return fmt.Errorf("error saving invoice: %v", err)
	}
	return nil
}

func (db *BoltDB) GetInvoice(paymentHash string) *Invoice {
	var invoice *Invoice

	db.bolt.View(func(tx *bolt.Tx) error {
		invoicesb := tx.Bucket([]byte(invoicesBucket))
		invoiceBytes := invoicesb.Get([]byte(paymentHash))
		err := json.Unmarshal(invoiceBytes, &invoice)
		if err != nil {
			invoice = nil
		}

		return nil
	})
	return invoice
}

func (db *BoltDB) GetInvoices() []Invoice {
	var invoices []Invoice

	db.bolt.View(func(tx *bolt.Tx) error {
		invoicesb := tx.Bucket([]byte(invoicesBucket))

		c := invoicesb.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var invoice Invoice
			if err := json.Unmarshal(v, &invoice); err != nil {
				invoices = []Invoice{}
				return nil
			}
			invoices = append(invoices, invoice)
		}
		return nil
	})
	return invoices
}
