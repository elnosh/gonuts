package nut10

import (
	"encoding/json"
	"fmt"

	"github.com/elnosh/gonuts/cashu"
)

type WellKnownSecret struct {
	Nonce string     `json:"nonce"`
	Data  string     `json:"data"`
	Tags  [][]string `json:"tags"`
}

// SerializeSecret returns the json string to be put in the secret field of a proof
func SerializeSecret(kind cashu.SecretKind, secretData WellKnownSecret) (string, error) {
	jsonSecret, err := json.Marshal(secretData)
	if err != nil {
		return "", err
	}

	secretKind := kind.String()
	secret := fmt.Sprintf("[\"%s\", %v]", secretKind, string(jsonSecret))

	return secret, nil
}
