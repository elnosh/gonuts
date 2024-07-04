package nut11

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
)

type P2PKWitness struct {
	Signatures []string `json:"signatures"`
}

// P2PKSecret returns a secret with a spending condition
// that will lock ecash to a public key
func P2PKSecret(pubkey string) (string, error) {
	// generate random nonce
	nonceBytes := make([]byte, 32)
	_, err := rand.Read(nonceBytes)
	if err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(nonceBytes)

	secretData := nut10.WellKnownSecret{
		Nonce: nonce,
		Data:  pubkey,
	}

	secret, err := nut10.SerializeSecret(cashu.P2PK, secretData)
	if err != nil {
		return "", err
	}

	return secret, nil
}
