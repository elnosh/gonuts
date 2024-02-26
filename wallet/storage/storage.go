package storage

import (
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
)

type DB interface {
	SaveProof(cashu.Proof) error
	//GetProofs(ids []string) cashu.Proofs
	GetProofs() cashu.Proofs
	DeleteProof(string) error
	SaveKeyset(crypto.Keyset) error
	GetKeysetsByMint(mintURL string) ([]crypto.Keyset, error) 
	GetKeysets() crypto.KeysetsMap
	SaveInvoice(lightning.Invoice) error
	GetInvoice(string) *lightning.Invoice
}
