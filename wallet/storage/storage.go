package storage

import (
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
)

type DB interface {
	GetProofs(ids []string) cashu.Proofs
	SaveProof(cashu.Proof) error
	DeleteProof(string) error
	SaveKeyset(crypto.Keyset) error
	GetKeysets() crypto.KeysetsMap
	SaveInvoice(lightning.Invoice) error
	GetInvoice(string) *lightning.Invoice
}
