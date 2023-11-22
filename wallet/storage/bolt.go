package storage

import (
	bolt "go.etcd.io/bbolt"
)

const (
	keysetsBucket = "keysets"
	proofsBucket  = "proofs"
)

type BoltDB struct {
	bolt *bolt.DB
}

func (db *BoltDB) initWalletBuckets() error {
	return db.bolt.Update(func(tx *bolt.Tx) error {
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
