package storage

import (
	"github.com/elnosh/gonuts/cashurpc"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
)

type DB interface {
	GetProofs(ids []string) cashurpc.Proofs
	SaveProof(*cashurpc.Proof) error
	DeleteProof(string) error
	SaveKeyset(crypto.Keyset) error
	GetKeysets() crypto.KeysetsMap
	SaveInvoice(lightning.Invoice) error
	GetInvoice(string) *lightning.Invoice
}
