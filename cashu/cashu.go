package cashu

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/crypto"
)

type BlindedMessage struct {
	Amount uint64 `json:"amount"`
	B_     string `json:"B_"`
	Id     string `json:"id"`
}

type BlindedMessages []BlindedMessage

type BlindedSignature struct {
	Amount uint64 `json:"amount"`
	C_     string `json:"C_"`
	Id     string `json:"id"`
}

type BlindedSignatures []BlindedSignature

type Proof struct {
	Amount uint64 `json:"amount"`
	Secret string `json:"secret"`
	C      string `json:"C"`
	// keyset id
	Id string `json:"id"`
}

type Proofs []Proof

type Token struct {
	Token []TokenProof `json:"token"`
	Unit  string       `json:"unit"`
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

	base64Bytes, err := base64.StdEncoding.DecodeString(base64Token)
	if err != nil {
		return nil, fmt.Errorf("error decoding token: %v", err)
	}

	var token Token
	err = json.Unmarshal(base64Bytes, &token)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling token: %v", err)
	}

	return &token, nil
}

func (t *Token) ToString() string {
	jsonBytes, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}

	token := base64.StdEncoding.EncodeToString(jsonBytes)
	return "cashuA" + strings.ReplaceAll(strings.ReplaceAll(token, "/", "_"), "+", "-")
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

func NewBlindedMessage(id string, amount uint64, B_ *secp256k1.PublicKey) BlindedMessage {
	B_str := hex.EncodeToString(B_.SerializeCompressed())
	return BlindedMessage{Amount: amount, B_: B_str, Id: id}
}

// returns Blinded messages, secrets - [][]byte, and list of r
func CreateBlindedMessages(amount uint64, keyset crypto.Keyset) (BlindedMessages, [][]byte, []*secp256k1.PrivateKey, error) {
	splitAmounts := AmountSplit(amount)
	splitLen := len(splitAmounts)

	blindedMessages := make(BlindedMessages, splitLen)
	secrets := make([][]byte, splitLen)
	rs := make([]*secp256k1.PrivateKey, splitLen)

	for i, amt := range splitAmounts {
		// create random secret
		secret := make([]byte, 32)
		_, err := rand.Read(secret)
		if err != nil {
			return nil, nil, nil, err
		}
		secretStr := hex.EncodeToString(secret)

		// generate new private key r
		r, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			return nil, nil, nil, err
		}

		B_, r := crypto.BlindMessage(secretStr, r)
		blindedMessage := NewBlindedMessage(keyset.Id, amt, B_)
		blindedMessages[i] = blindedMessage
		secrets[i] = secret
		rs[i] = r
	}

	return blindedMessages, secrets, rs, nil
}
