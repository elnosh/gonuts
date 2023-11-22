package wallet

import (
	"encoding/json"
	"fmt"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	bolt "go.etcd.io/bbolt"
)

const (
	keysetsBucket = "keysets"
	proofsBucket  = "proofs"
)

func (w *Wallet) initWalletBuckets() error {
	return w.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucket([]byte(keysetsBucket))
		if err != nil {
			return err
		}

		_, err = tx.CreateBucket([]byte(proofsBucket))
		if err != nil {
			return err
		}

		return nil
	})
}

func (w *Wallet) getProofs() cashu.Proofs {
	proofs := cashu.Proofs{}

	if err := w.db.View(func(tx *bolt.Tx) error {
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

func (w *Wallet) getKeysets() []crypto.Keyset {
	keysets := []crypto.Keyset{}

	if err := w.db.View(func(tx *bolt.Tx) error {
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
