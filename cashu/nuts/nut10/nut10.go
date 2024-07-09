package nut10

import (
	"encoding/json"
	"errors"
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

// DeserializeSecret returns Well-known secret struct.
// It returns error if it's not valid according to NUT-10
func DeserializeSecret(secret string) (WellKnownSecret, error) {
	var rawJsonSecret []json.RawMessage
	if err := json.Unmarshal([]byte(secret), &rawJsonSecret); err != nil {
		return WellKnownSecret{}, err
	}

	// Well-known secret should have a length of at least 2
	if len(rawJsonSecret) < 2 {
		return WellKnownSecret{}, errors.New("invalid secret: length < 2")
	}

	var kind string
	if err := json.Unmarshal(rawJsonSecret[0], &kind); err != nil {
		return WellKnownSecret{}, errors.New("invalid kind for secret")
	}

	var secretData WellKnownSecret
	if err := json.Unmarshal(rawJsonSecret[1], &secretData); err != nil {
		return WellKnownSecret{}, fmt.Errorf("invalid secret: %v", err)
	}

	return secretData, nil
}
