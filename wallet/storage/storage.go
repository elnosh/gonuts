package storage

import (
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
)

type DB interface {
	GetProofs() cashu.Proofs
	SaveProof(cashu.Proof) error
	GetKeysets() []crypto.Keyset
	SaveInvoice(lightning.Invoice) error
	GetInvoice(string) *lightning.Invoice
}
