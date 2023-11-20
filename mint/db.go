package mint

import (
	"encoding/json"

	"github.com/elnosh/gonuts/crypto"
	bolt "go.etcd.io/bbolt"
)

const (
	keysetsBucket = "keysets"
)

func (m *Mint) InitKeysetsBucket(keyset crypto.Keyset) error {
	return m.db.Update(func(tx *bolt.Tx) error {
		keysets, err := tx.CreateBucketIfNotExists([]byte(keysetsBucket))
		if err != nil {
			return err
		}

		jsonKeyset, err := json.Marshal(keyset)
		if err != nil {
			return err
		}

		err = keysets.Put([]byte(keyset.Id), jsonKeyset)
		if err != nil {
			return err
		}

		return nil
	})
}

func (m *Mint) GetKeysets() []*crypto.Keyset {
	keysets := []*crypto.Keyset{}

	m.db.View(func(tx *bolt.Tx) error {
		keysetsBucket := tx.Bucket([]byte(keysetsBucket))

		c := keysetsBucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var keyset crypto.Keyset
			if err := json.Unmarshal(v, &keyset); err != nil {
				break
			}
			keysets = append(keysets, &keyset)
		}
		return nil
	})

	return keysets
}
