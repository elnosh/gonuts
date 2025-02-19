package storage

import (
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut05"
)

type MintDB interface {
	SaveSeed([]byte) error
	GetSeed() ([]byte, error)

	SaveKeyset(DBKeyset) error
	GetKeysets() ([]DBKeyset, error)
	UpdateKeysetActive(keysetId string, active bool) error

	SaveProofs(cashu.Proofs) error
	GetProofsUsed(Ys []string) ([]DBProof, error)
	AddPendingProofs(proofs cashu.Proofs, quoteId string) error
	GetPendingProofs(Ys []string) ([]DBProof, error)
	GetPendingProofsByQuote(quoteId string) ([]DBProof, error)
	RemovePendingProofs(Ys []string) error

	SaveMintQuote(MintQuote) error
	GetMintQuote(string) (MintQuote, error)
	GetMintQuoteByPaymentHash(string) (MintQuote, error)
	UpdateMintQuoteState(quoteId string, state nut04.State) error

	SaveMeltQuote(MeltQuote) error
	GetMeltQuote(string) (MeltQuote, error)
	// used to check if a melt quote already exists for the passed invoice
	GetMeltQuoteByPaymentRequest(string) (*MeltQuote, error)
	UpdateMeltQuote(quoteId string, preimage string, state nut05.State) error

	SaveBlindSignatures(B_s []string, blindSignatures cashu.BlindedSignatures) error
	GetBlindSignature(B_ string) (cashu.BlindedSignature, error)
	GetBlindSignatures(B_s []string) (cashu.BlindedSignatures, error)

	// these return a map of keyset id and amount
	GetIssuedEcash() (map[string]uint64, error)
	GetRedeemedEcash() (map[string]uint64, error)

	Close() error
}

type DBKeyset struct {
	Id                string
	Unit              string
	Active            bool
	Seed              string
	DerivationPathIdx uint32
	InputFeePpk       uint
}

type DBProof struct {
	Amount  uint64
	Id      string
	Secret  string
	Y       string
	C       string
	Witness string
	// for proofs in pending table
	MeltQuoteId string
}

type MintQuote struct {
	Id             string
	Amount         uint64
	PaymentRequest string
	PaymentHash    string
	State          nut04.State
	Expiry         uint64
	Pubkey         *secp256k1.PublicKey
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
	IsMpp          bool
	// used when the melt quote is MPP
	AmountMsat uint64
}
