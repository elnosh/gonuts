package storage

import (
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
)

type QuoteType int

const (
	Mint QuoteType = iota + 1
	Melt
)

func (quote QuoteType) String() string {
	switch quote {
	case Mint:
		return "Mint"
	case Melt:
		return "Melt"
	default:
		return "unknown"
	}
}

type WalletDB interface {
	SaveMnemonicSeed(string, []byte)
	GetSeed() []byte
	GetMnemonic() string

	SaveProofs(cashu.Proofs) error
	GetProofsByKeysetId(string) cashu.Proofs
	GetProofs() cashu.Proofs
	DeleteProof(string) error

	SaveKeyset(*crypto.WalletKeyset) error
	GetKeysets() crypto.KeysetsMap
	GetKeyset(string) *crypto.WalletKeyset
	IncrementKeysetCounter(string, uint32) error
	GetKeysetCounter(string) uint32

	SaveInvoice(Invoice) error
	GetInvoice(string) *Invoice
	GetInvoices() []Invoice
}

type Invoice struct {
	TransactionType QuoteType
	// mint or melt quote id
	Id             string
	QuoteAmount    uint64
	PaymentRequest string
	PaymentHash    string
	Preimage       string
	CreatedAt      int64
	Paid           bool
	SettledAt      int64
	InvoiceAmount  uint64
	QuoteExpiry    uint64
}
