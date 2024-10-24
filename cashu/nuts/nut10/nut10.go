package nut10

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/elnosh/gonuts/cashu"
)

type SecretKind int

const (
	AnyoneCanSpend SecretKind = iota
	P2PK
	HTLC
)

func SecretType(proof cashu.Proof) SecretKind {
	var rawJsonSecret []json.RawMessage
	// if not valid json, assume it is random secret
	if err := json.Unmarshal([]byte(proof.Secret), &rawJsonSecret); err != nil {
		return AnyoneCanSpend
	}

	// Well-known secret should have a length of at least 2
	if len(rawJsonSecret) < 2 {
		return AnyoneCanSpend
	}

	var kind string
	if err := json.Unmarshal(rawJsonSecret[0], &kind); err != nil {
		return AnyoneCanSpend
	}

	switch kind {
	case "P2PK":
		return P2PK
	case "HTLC":
		return HTLC
	}

	return AnyoneCanSpend
}

func (kind SecretKind) String() string {
	switch kind {
	case P2PK:
		return "P2PK"
	case HTLC:
		return "HTLC"
	default:
		return "anyonecanspend"
	}
}

type WellKnownSecret struct {
	Nonce string     `json:"nonce"`
	Data  string     `json:"data"`
	Tags  [][]string `json:"tags"`
}

// SerializeSecret returns the json string to be put in the secret field of a proof
func SerializeSecret(kind SecretKind, secretData WellKnownSecret) (string, error) {
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

type SpendingCondition struct {
	Kind SecretKind
	Data string
	Tags [][]string
}

func NewSecretFromSpendingCondition(spendingCondition SpendingCondition) (string, error) {
	// generate random nonce
	nonceBytes := make([]byte, 32)
	_, err := rand.Read(nonceBytes)
	if err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(nonceBytes)

	secretData := WellKnownSecret{
		Nonce: nonce,
		Data:  spendingCondition.Data,
		Tags:  spendingCondition.Tags,
	}

	if spendingCondition.Kind != P2PK && spendingCondition.Kind != HTLC {
		return "", fmt.Errorf("invalid NUT-10 kind '%s' to create new secret", spendingCondition.Kind)
	}

	secret, err := SerializeSecret(spendingCondition.Kind, secretData)
	if err != nil {
		return "", err
	}

	return secret, nil
}
