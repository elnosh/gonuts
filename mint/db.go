package mint

import (
	"encoding/json"
	"fmt"

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
)

func (m *Mint) initMintBuckets() error {
	return m.db.Update(func(tx *bolt.Tx) error {
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

		return nil
	})
}

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

func (m *Mint) GetKeysets() []crypto.Keyset {
	keysets := []crypto.Keyset{}

	m.db.View(func(tx *bolt.Tx) error {
		keysetsBucket := tx.Bucket([]byte(keysetsBucket))

		c := keysetsBucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var keyset crypto.Keyset
			if err := json.Unmarshal(v, &keyset); err != nil {
				break
			}
			keysets = append(keysets, keyset)
		}
		return nil
	})

	return keysets
}

func (m *Mint) InitProofsBucket() {
	m.db.Update(func(tx *bolt.Tx) error {
		_, _ = tx.CreateBucketIfNotExists([]byte(proofsBucket))
		return nil
	})
}

func (m *Mint) GetProof(secret string) *cashu.Proof {
	var proof *cashu.Proof

	m.db.View(func(tx *bolt.Tx) error {
		proofsb := tx.Bucket([]byte(proofsBucket))
		proofBytes := proofsb.Get([]byte(secret))
		err := json.Unmarshal(proofBytes, &proof)
		if err != nil {
			proof = nil
		}
		return nil
	})
	return proof
}

func (m *Mint) SaveProof(proof cashu.Proof) error {
	jsonProof, err := json.Marshal(proof)
	if err != nil {
		return fmt.Errorf("invalid proof format: %v", err)
	}

	if err := m.db.Update(func(tx *bolt.Tx) error {
		proofsb := tx.Bucket([]byte(proofsBucket))
		key := []byte(proof.Secret)
		return proofsb.Put(key, jsonProof)
	}); err != nil {
		return fmt.Errorf("error saving proof: %v", err)
	}
	return nil
}

func (m *Mint) InitInvoiceBucket() {
	m.db.Update(func(tx *bolt.Tx) error {
		_, _ = tx.CreateBucketIfNotExists([]byte(invoicesBucket))
		return nil
	})
}

func (m *Mint) SaveInvoice(invoice lightning.Invoice) error {
	jsonbytes, err := json.Marshal(invoice)
	if err != nil {
		return fmt.Errorf("invalid invoice: %v", err)
	}

	if err := m.db.Update(func(tx *bolt.Tx) error {
		invoicesb := tx.Bucket([]byte(invoicesBucket))
		key := []byte(invoice.Id)
		err := invoicesb.Put(key, jsonbytes)
		return err
	}); err != nil {
		return fmt.Errorf("error saving invoice: %v", err)
	}
	return nil
}

func (m *Mint) GetInvoice(id string) *lightning.Invoice {
	var invoice *lightning.Invoice

	m.db.View(func(tx *bolt.Tx) error {
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
