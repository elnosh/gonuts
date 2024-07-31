package storage

import (
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
)

type MintDB interface {
	GetBalance() (uint64, error)

	SaveSeed([]byte) error
	GetSeed() ([]byte, error)

	SaveKeyset(DBKeyset) error
	GetKeysets() ([]DBKeyset, error)
	UpdateKeysetActive(keysetId string, active bool) error

	SaveProofs(cashu.Proofs) error
	GetProofsUsed(Ys []string) ([]cashu.Proof, error)

	SaveMintQuote(MintQuote) error
	GetMintQuote(string) (*MintQuote, error)
	UpdateMintQuoteState(quoteId string, state nut04.State) error

	SaveMeltQuote(MeltQuote) error
	GetMeltQuote(string) (*MeltQuote, error)
	UpdateMeltQuote(quoteId string, preimage string, state nut05.State) error
}

type DBKeyset struct {
	Id                string
	Unit              string
	Active            bool
	Seed              string
	DerivationPathIdx uint32
	InputFeePpk       uint
}

type MintQuote struct {
	Id             string
	Amount         uint64
	PaymentRequest string
	PaymentHash    string
	State          nut04.State
	Expiry         uint64
}

type MeltQuote struct {
	Id             string
	InvoiceRequest string
	PaymentHash    string
	Amount         uint64
	FeeReserve     uint64
	State          nut05.State
	Expiry         uint64
	Preimage       string
}
