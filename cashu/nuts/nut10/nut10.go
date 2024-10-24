package nut10

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

type SecretKind int

const (
	AnyoneCanSpend SecretKind = iota
	P2PK
	HTLC
)

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
	Kind SecretKind
	Data SecretData
}

type SecretData struct {
	Nonce string     `json:"nonce"`
	Data  string     `json:"data"`
	Tags  [][]string `json:"tags"`
}

// SerializeSecret returns the json string to be put in the secret field of a proof
func SerializeSecret(secret WellKnownSecret) (string, error) {
	jsonSecret, err := json.Marshal(secret.Data)
	if err != nil {
		return "", err
	}

	serializedSecret := fmt.Sprintf("[\"%s\", %v]", secret.Kind, string(jsonSecret))
	return serializedSecret, nil
}

// DeserializeSecret returns Well-known secret struct.
// It returns error if it's not valid according to NUT-10
func DeserializeSecret(serializedSecret string) (WellKnownSecret, error) {
	var rawJsonSecret []json.RawMessage
	if err := json.Unmarshal([]byte(serializedSecret), &rawJsonSecret); err != nil {
		return WellKnownSecret{}, err
	}

	// Well-known secret should have a length of at least 2
	if len(rawJsonSecret) < 2 {
		return WellKnownSecret{}, errors.New("invalid secret: length < 2")
	}

	var kind string
	var secret WellKnownSecret
	if err := json.Unmarshal(rawJsonSecret[0], &kind); err != nil {
		return WellKnownSecret{}, errors.New("invalid kind for secret")
	}

	switch kind {
	case "P2PK":
		secret.Kind = P2PK
	case "HTLC":
		secret.Kind = HTLC
	default:
		secret.Kind = AnyoneCanSpend
	}

	if err := json.Unmarshal(rawJsonSecret[1], &secret.Data); err != nil {
		return WellKnownSecret{}, fmt.Errorf("invalid secret: %v", err)
	}

	return secret, nil
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

	if spendingCondition.Kind != P2PK && spendingCondition.Kind != HTLC {
		return "", fmt.Errorf("invalid NUT-10 kind '%s' to create new secret", spendingCondition.Kind)
	}

	secretData := WellKnownSecret{
		Kind: spendingCondition.Kind,
		Data: SecretData{
			Nonce: nonce,
			Data:  spendingCondition.Data,
			Tags:  spendingCondition.Tags,
		},
	}

	secret, err := SerializeSecret(secretData)
	if err != nil {
		return "", err
	}

	return secret, nil
}
