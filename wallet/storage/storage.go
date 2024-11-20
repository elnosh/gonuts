package storage

import (
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
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
	GetProofs() cashu.Proofs
	GetProofsByKeysetId(string) cashu.Proofs
	DeleteProof(string) error

	AddPendingProofsByQuoteId(cashu.Proofs, string) error
	GetPendingProofs() []DBProof
	GetPendingProofsByQuoteId(string) []DBProof
	DeletePendingProofsByQuoteId(string) error

	SaveKeyset(*crypto.WalletKeyset) error
	GetKeysets() crypto.KeysetsMap
	GetKeyset(string) *crypto.WalletKeyset
	IncrementKeysetCounter(string, uint32) error
	GetKeysetCounter(string) uint32

	SaveMintQuote(MintQuote) error
	GetMintQuotes() []MintQuote
	GetMintQuoteById(string) *MintQuote

	SaveMeltQuote(MeltQuote) error
	GetMeltQuotes() []MeltQuote
	GetMeltQuoteById(string) *MeltQuote
}

type DBProof struct {
	Y      string           `json:"y"`
	Amount uint64           `json:"amount"`
	Id     string           `json:"id"`
	Secret string           `json:"secret"`
	C      string           `json:"C"`
	DLEQ   *cashu.DLEQProof `json:"dleq,omitempty"`
	// set if proofs are tied to a melt quote
	MeltQuoteId string `json:"quote_id"`
}

type MintQuote struct {
	QuoteId        string
	Mint           string
	Method         string
	State          nut04.State
	Unit           string
	Amount         uint64
	PaymentRequest string
	CreatedAt      int64
	SettledAt      int64
	QuoteExpiry    uint64
}

type MeltQuote struct {
	QuoteId        string
	Mint           string
	Method         string
	State          nut05.State
	Unit           string
	PaymentRequest string
	Amount         uint64
	FeeReserve     uint64
	Preimage       string
	SettledAt      int64
	QuoteExpiry    uint64
}

type Invoice struct {
	TransactionType QuoteType
	// mint or melt quote id
	Id string
	// mint that issued quote
	Mint           string
	QuoteAmount    uint64
	InvoiceAmount  uint64
	PaymentRequest string
	PaymentHash    string
	Preimage       string
	CreatedAt      int64
	Paid           bool
	SettledAt      int64
	QuoteExpiry    uint64
}
