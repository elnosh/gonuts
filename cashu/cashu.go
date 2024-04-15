// Package cashu contains the core structs and logic
// of the Cashu protocol.
package cashu

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// Cashu BlindedMessage. See https://github.com/cashubtc/nuts/blob/main/00.md#blindedmessage
type BlindedMessage struct {
	Amount uint64 `json:"amount"`
	B_     string `json:"B_"`
	Id     string `json:"id"`

	// including Witness field for now to avoid throwing error when parsing json
	// from clients that include this field even when mint does not support it.
	Witness string `json:"witness,omitempty"`
}

type BlindedMessages []BlindedMessage

// Cashu BlindedSignature. See https://github.com/cashubtc/nuts/blob/main/00.md#blindsignature
type BlindedSignature struct {
	Amount uint64 `json:"amount"`
	C_     string `json:"C_"`
	Id     string `json:"id"`
}

type BlindedSignatures []BlindedSignature

// Cashu Proof. See https://github.com/cashubtc/nuts/blob/main/00.md#proof
type Proof struct {
	Amount uint64 `json:"amount"`
	Id     string `json:"id"`
	Secret string `json:"secret"`
	C      string `json:"C"`

	// including Witness field for now to avoid throwing error when parsing json
	// from clients that include this field even when mint does not support it.
	Witness string `json:"witness,omitempty"`
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

// Error represents an error to be used by the mint
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
	AmountsDoNotMatch        = Error{Detail: "amounts do not match", Code: ProofsErrCode}
	MeltQuoteNotExistErr     = Error{Detail: "melt quote does not exist", Code: QuoteErrCode}
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
