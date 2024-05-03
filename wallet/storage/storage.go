package storage

import (
	cashurpc "buf.build/gen/go/cashu/rpc/protocolbuffers/go"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/mint/lightning"
)

type DB interface {
	SaveProof(*cashurpc.Proof) error
	GetProofsByKeysetId(string) []*cashurpc.Proof
	GetProofs() []*cashurpc.Proof
	DeleteProof(string) error
	SaveKeyset(*crypto.Keyset) error
	GetKeysets() crypto.KeysetsMap
	SaveInvoice(lightning.Invoice) error
	GetInvoice(string) *lightning.Invoice
	GetInvoices() []lightning.Invoice
}
