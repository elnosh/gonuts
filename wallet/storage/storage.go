package storage

import (
	"encoding/json"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
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

	AddPendingProofs(cashu.Proofs) error
	AddPendingProofsByQuoteId(cashu.Proofs, string) error
	GetPendingProofs() []DBProof
	GetPendingProofsByQuoteId(string) []DBProof
	DeletePendingProofs([]string) error
	DeletePendingProofsByQuoteId(string) error

	SaveKeyset(*crypto.WalletKeyset) error
	GetKeysets() crypto.KeysetsMap
	GetKeyset(string) *crypto.WalletKeyset
	IncrementKeysetCounter(string, uint32) error
	GetKeysetCounter(string) uint32
	UpdateKeysetMintURL(oldURL, newURL string) error

	SaveMintQuote(MintQuote) error
	GetMintQuotes() []MintQuote
	GetMintQuoteById(string) *MintQuote

	SaveMeltQuote(MeltQuote) error
	GetMeltQuotes() []MeltQuote
	GetMeltQuoteById(string) *MeltQuote

	Close() error
}

type DBProof struct {
	Y      string           `json:"y"`
	Amount uint64           `json:"amount"`
	Id     string           `json:"id"`
	Secret string           `json:"secret"`
	C      string           `json:"C"`
	DLEQ   *cashu.DLEQProof `json:"dleq,omitempty"`
	// set if pending proofs are tied to a melt quote
	MeltQuoteId string `json:"quote_id"`
}

type MintQuote struct {
	QuoteId        string
	Mint           string
	Method         string
	State          nut04.State
	Unit           string
	PaymentRequest string
	Amount         uint64
	CreatedAt      int64
	SettledAt      int64
	QuoteExpiry    uint64
	PrivateKey     *secp256k1.PrivateKey
}

type mintQuoteTemp struct {
	QuoteId        string
	Mint           string
	Method         string
	State          nut04.State
	Unit           string
	PaymentRequest string
	Amount         uint64
	CreatedAt      int64
	SettledAt      int64
	QuoteExpiry    uint64
	PrivateKey     []byte
}

// custom Marshaller to serialize and deserialize private key to and from []byte

func (mq *MintQuote) MarshalJSON() ([]byte, error) {
	tempQuote := mintQuoteTemp{
		QuoteId:        mq.QuoteId,
		Mint:           mq.Mint,
		Method:         mq.Method,
		State:          mq.State,
		Unit:           mq.Unit,
		PaymentRequest: mq.PaymentRequest,
		Amount:         mq.Amount,
		CreatedAt:      mq.CreatedAt,
		SettledAt:      mq.SettledAt,
		QuoteExpiry:    mq.QuoteExpiry,
	}

	if mq.PrivateKey != nil {
		tempQuote.PrivateKey = mq.PrivateKey.Serialize()
	}

	return json.Marshal(tempQuote)
}

func (mq *MintQuote) UnmarshalJSON(data []byte) error {
	tempQuote := &mintQuoteTemp{}

	if err := json.Unmarshal(data, tempQuote); err != nil {
		return err
	}

	mq.QuoteId = tempQuote.QuoteId
	mq.Mint = tempQuote.Mint
	mq.Method = tempQuote.Method
	mq.State = tempQuote.State
	mq.Unit = tempQuote.Unit
	mq.PaymentRequest = tempQuote.PaymentRequest
	mq.Amount = tempQuote.Amount
	mq.CreatedAt = tempQuote.CreatedAt
	mq.SettledAt = tempQuote.SettledAt
	mq.QuoteExpiry = tempQuote.QuoteExpiry
	if len(tempQuote.PrivateKey) > 0 {
		mq.PrivateKey = secp256k1.PrivKeyFromBytes(tempQuote.PrivateKey)
	}

	return nil
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
	CreatedAt      int64
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
