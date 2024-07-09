// Package cashu contains the core structs and logic
// of the Cashu protocol.
package cashu

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

type SecretKind int

const (
	Random SecretKind = iota
	P2PK
)

// Cashu BlindedMessage. See https://github.com/cashubtc/nuts/blob/main/00.md#blindedmessage
type BlindedMessage struct {
	Amount  uint64 `json:"amount"`
	B_      string `json:"B_"`
	Id      string `json:"id"`
	Witness string `json:"witness,omitempty"`
}

func NewBlindedMessage(id string, amount uint64, B_ *secp256k1.PublicKey) BlindedMessage {
	B_str := hex.EncodeToString(B_.SerializeCompressed())
	return BlindedMessage{Amount: amount, B_: B_str, Id: id}
}

func SortBlindedMessages(blindedMessages BlindedMessages, secrets []string, rs []*secp256k1.PrivateKey) {
	// sort messages, secrets and rs
	for i := 0; i < len(blindedMessages)-1; i++ {
		for j := i + 1; j < len(blindedMessages); j++ {
			if blindedMessages[i].Amount > blindedMessages[j].Amount {
				// Swap blinded messages
				blindedMessages[i], blindedMessages[j] = blindedMessages[j], blindedMessages[i]

				// Swap secrets
				secrets[i], secrets[j] = secrets[j], secrets[i]

				// Swap rs
				rs[i], rs[j] = rs[j], rs[i]
			}
		}
	}
}

type BlindedMessages []BlindedMessage

func (bm BlindedMessages) Amount() uint64 {
	var totalAmount uint64 = 0
	for _, msg := range bm {
		totalAmount += msg.Amount
	}
	return totalAmount
}

// Cashu BlindedSignature. See https://github.com/cashubtc/nuts/blob/main/00.md#blindsignature
type BlindedSignature struct {
	Amount uint64 `json:"amount"`
	C_     string `json:"C_"`
	Id     string `json:"id"`
}

type BlindedSignatures []BlindedSignature

// Cashu Proof. See https://github.com/cashubtc/nuts/blob/main/00.md#proof
type Proof struct {
	Amount  uint64 `json:"amount"`
	Id      string `json:"id"`
	Secret  string `json:"secret"`
	C       string `json:"C"`
	Witness string `json:"witness,omitempty"`
}

func (p Proof) IsSecretP2PK() bool {
	return p.SecretType() == P2PK
}

func (p Proof) SecretType() SecretKind {
	var rawJsonSecret []json.RawMessage
	// if not valid json, assume it is random secret
	if err := json.Unmarshal([]byte(p.Secret), &rawJsonSecret); err != nil {
		return Random
	}

	// Well-known secret should have a length of at least 2
	if len(rawJsonSecret) < 2 {
		return Random
	}

	var kind string
	if err := json.Unmarshal(rawJsonSecret[0], &kind); err != nil {
		return Random
	}

	if kind == "P2PK" {
		return P2PK
	}

	return Random
}

func (kind SecretKind) String() string {
	switch kind {
	case P2PK:
		return "P2PK"
	default:
		return "random"
	}
}

type Proofs []Proof

// Amount returns the total amount from
// the array of Proof
func (proofs Proofs) Amount() uint64 {
	var totalAmount uint64 = 0
	for _, proof := range proofs {
		totalAmount += proof.Amount
	}
	return totalAmount
}

// Cashu token. See https://github.com/cashubtc/nuts/blob/main/00.md#token-format
type Token struct {
	Token []TokenProof `json:"token"`
	Unit  string       `json:"unit"`
	Memo  string       `json:"memo,omitempty"`
}

type TokenProof struct {
	Mint   string `json:"mint"`
	Proofs Proofs `json:"proofs"`
}

func NewToken(proofs Proofs, mint string, unit string) Token {
	tokenProof := TokenProof{Mint: mint, Proofs: proofs}
	return Token{Token: []TokenProof{tokenProof}, Unit: unit}
}

func DecodeToken(tokenstr string) (*Token, error) {
	prefixVersion := tokenstr[:6]
	base64Token := tokenstr[6:]
	if prefixVersion != "cashuA" {
		return nil, errors.New("invalid token")
	}

	var tokenBytes []byte
	var err error
	tokenBytes, err = base64.URLEncoding.DecodeString(base64Token)
	if err != nil {
		tokenBytes, err = base64.RawURLEncoding.DecodeString(base64Token)
		if err != nil {
			return nil, fmt.Errorf("error decoding token: %v", err)
		}
	}

	var token Token
	err = json.Unmarshal(tokenBytes, &token)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling token: %v", err)
	}

	return &token, nil
}

// ToString serializes the token to a string
func (t *Token) ToString() string {
	jsonBytes, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}

	token := base64.URLEncoding.EncodeToString(jsonBytes)
	return "cashuA" + token
}

// TotalAmount returns the total amount
// from the array of Proofs in the token
func (t *Token) TotalAmount() uint64 {
	var totalAmount uint64 = 0
	for _, tokenProof := range t.Token {
		for _, proof := range tokenProof.Proofs {
			totalAmount += proof.Amount
		}
	}
	return totalAmount
}

type CashuErrCode int

// Error represents an error to be returned by the mint
type Error struct {
	Detail string       `json:"detail"`
	Code   CashuErrCode `json:"code"`
}

func BuildCashuError(detail string, code CashuErrCode) *Error {
	return &Error{Detail: detail, Code: code}
}

func (e Error) Error() string {
	return e.Detail
}

// Common error codes
const (
	StandardErrCode CashuErrCode = 1000 + iota
	KeysetErrCode
	PaymentMethodErrCode
	UnitErrCode
	QuoteErrCode
	InvoiceErrCode
	ProofsErrCode
)

var (
	StandardErr                  = Error{Detail: "unable to process request", Code: StandardErrCode}
	EmptyBodyErr                 = Error{Detail: "request body cannot be emtpy", Code: StandardErrCode}
	KeysetNotExistErr            = Error{Detail: "keyset does not exist", Code: KeysetErrCode}
	PaymentMethodNotSupportedErr = Error{Detail: "payment method not supported", Code: PaymentMethodErrCode}
	UnitNotSupportedErr          = Error{Detail: "unit not supported", Code: UnitErrCode}
	InvalidBlindedMessageAmount  = Error{Detail: "invalid amount in blinded message", Code: KeysetErrCode}
	QuoteIdNotSpecifiedErr       = Error{Detail: "quote id not specified", Code: QuoteErrCode}
	InvoiceNotExistErr           = Error{Detail: "invoice does not exist", Code: InvoiceErrCode}
	InvoiceNotPaidErr            = Error{Detail: "invoice has not been paid", Code: InvoiceErrCode}
	OutputsOverInvoiceErr        = Error{
		Detail: "sum of the output amounts is greater than amount of invoice paid",
		Code:   InvoiceErrCode}
	InvoiceTokensIssuedErr   = Error{Detail: "tokens already issued for invoice", Code: InvoiceErrCode}
	ProofAlreadyUsedErr      = Error{Detail: "proofs already used", Code: ProofsErrCode}
	InvalidProofErr          = Error{Detail: "invalid proof", Code: ProofsErrCode}
	InputsBelowOutputs       = Error{Detail: "amount of input proofs is below amount of outputs", Code: ProofsErrCode}
	QuoteNotExistErr         = Error{Detail: "quote does not exist", Code: QuoteErrCode}
	QuoteAlreadyPaid         = Error{Detail: "quote already paid", Code: QuoteErrCode}
	InsufficientProofsAmount = Error{Detail: "insufficient amount in proofs", Code: ProofsErrCode}
	InvalidKeysetProof       = Error{Detail: "proof from an invalid keyset", Code: ProofsErrCode}
	InvalidSignatureRequest  = Error{Detail: "requested signature from non-active keyset", Code: KeysetErrCode}
)

// Given an amount, it returns list of amounts e.g 13 -> [1, 4, 8]
// that can be used to build blinded messages or split operations.
// from nutshell implementation
func AmountSplit(amount uint64) []uint64 {
	rv := make([]uint64, 0)
	for pos := 0; amount > 0; pos++ {
		if amount&1 == 1 {
			rv = append(rv, 1<<pos)
		}
		amount >>= 1
	}
	return rv
}
