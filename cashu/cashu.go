package cashu

import (
	"crypto/rand"
	"encoding/hex"
	"log"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/crypto"
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
