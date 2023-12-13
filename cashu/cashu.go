package cashu

import (
	"crypto/rand"
	"encoding/hex"
	"log"

	"github.com/btcsuite/btcd/btcec/v2"
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
	Id     string `json:"id"`
}

type Proofs []Proof

type Error struct {
	Detail string `json:"detail"`
	Code   int    `json:"code"`
}

var (
	StandardErr               = Error{Detail: "unable to process request", Code: 1000}
	EmptyBody                 = Error{Detail: "request body cannot be emtpy", Code: 1001}
	KeysetsErr                = Error{Detail: "unable to serve keysets", Code: 1002}
	KeysetNotExist            = Error{Detail: "keyset does not exist", Code: 1003}
	PaymentMethodNotSpecified = Error{Detail: "payment method not specified", Code: 1004}
	PaymentMethodNotSupported = Error{Detail: "payment method not supported", Code: 1005}
	UnitNotSupported          = Error{Detail: "unit not supported", Code: 1006}
	QuoteIdNotSpecified       = Error{Detail: "quote id not specified", Code: 1007}
	InvoiceNotExist           = Error{Detail: "invoice does not exist", Code: 1008}
	InvoiceNotPaid            = Error{Detail: "invoice has not been paid", Code: 1009}
	OutputsOverInvoice        = Error{
		Detail: "sum of the output amounts is greater than amount of invoice paid",
		Code:   1010}
	InvoiceTokensIssued = Error{Detail: "tokens already issued for invoice", Code: 1011}
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

func NewBlindedMessage(amount uint64, B_ *secp256k1.PublicKey) BlindedMessage {
	B_str := hex.EncodeToString(B_.SerializeCompressed())
	return BlindedMessage{Amount: amount, B_: B_str}
}

// returns Blinded messages, secrets - [][]byte, and list of r
func CreateBlindedMessages(amount uint64) (BlindedMessages, [][]byte, []*secp256k1.PrivateKey, error) {
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

		// generate new private key r
		r, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			return nil, nil, nil, err
		}

		B_, r := crypto.BlindMessage(secret, r)
		blindedMessage := NewBlindedMessage(amt, B_)
		blindedMessages[i] = blindedMessage
		secrets[i] = secret
		rs[i] = r
	}

	return blindedMessages, secrets, rs, nil
}

func SignBlindedMessages(blinded BlindedMessages,
	keyset *crypto.Keyset) (BlindedSignatures, error) {

	blindedSignatures := BlindedSignatures{}

	for _, msg := range blinded {
		var privateKey []byte
		for _, kp := range keyset.KeyPairs {
			if kp.Amount == msg.Amount {
				privateKey = kp.PrivateKey
			}
		}

		privKey := secp256k1.PrivKeyFromBytes(privateKey)

		B_bytes, err := hex.DecodeString(msg.B_)
		if err != nil {
			log.Fatal(err)
		}
		B_, err := btcec.ParsePubKey(B_bytes)
		if err != nil {
			return nil, err
		}

		C_ := crypto.SignBlindedMessage(B_, privKey)
		C_hex := hex.EncodeToString(C_.SerializeCompressed())

		blindedSignature := BlindedSignature{Amount: msg.Amount,
			C_: C_hex, Id: keyset.Id}

		blindedSignatures = append(blindedSignatures, blindedSignature)
	}

	return blindedSignatures, nil
}
