package cashu

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elnosh/gonuts/cashurpc"
)

type BlindedMessages []*cashurpc.BlindedMessage

type BlindedSignatures []*cashurpc.BlindedSignature

func Amount(proofs []*cashurpc.Proof) uint64 {
	var totalAmount uint64 = 0
	for _, proof := range proofs {
		totalAmount += proof.Amount
	}
	return totalAmount
}

func NewToken(proofs []*cashurpc.Proof, mint string, unit string) cashurpc.TokenV3 {
	tokenProof := cashurpc.BaseToken{Mint: mint, Proofs: proofs}
	return cashurpc.TokenV3{Token: []*cashurpc.BaseToken{&tokenProof}, Unit: unit}
}

func DecodeToken(tokenstr string) (*cashurpc.TokenV3, error) {
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

	var token cashurpc.TokenV3
	err = json.Unmarshal(tokenBytes, &token)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling token: %v", err)
	}

	return &token, nil
}

type CashuErrCode int

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
