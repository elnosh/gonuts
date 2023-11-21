package cashu

import (
	"encoding/hex"
	"log"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/elnosh/gonuts/crypto"
)

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

		privKey, _ := btcec.PrivKeyFromBytes(privateKey)

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
