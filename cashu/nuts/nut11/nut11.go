package nut11

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
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

func AddSignatureToInputs(inputs cashu.Proofs, signingKey *btcec.PrivateKey) (cashu.Proofs, error) {
	for i, proof := range inputs {
		hash := sha256.Sum256([]byte(proof.Secret))
		signature, err := schnorr.Sign(signingKey, hash[:])
		if err != nil {
			return nil, err
		}
		signatureBytes := signature.Serialize()

		p2pkWitness := P2PKWitness{
			Signatures: []string{hex.EncodeToString(signatureBytes)},
		}

		witness, err := json.Marshal(p2pkWitness)
		if err != nil {
			return nil, err
		}
		proof.Witness = string(witness)
		inputs[i] = proof
	}

	return inputs, nil
}

func AddSignatureToOutputs(
	outputs cashu.BlindedMessages,
	signingKey *btcec.PrivateKey,
) (cashu.BlindedMessages, error) {
	for i, output := range outputs {
		msgToSign, err := hex.DecodeString(output.B_)
		if err != nil {
			return nil, err
		}

		hash := sha256.Sum256(msgToSign)
		signature, err := schnorr.Sign(signingKey, hash[:])
		if err != nil {
			return nil, err
		}
		signatureBytes := signature.Serialize()

		p2pkWitness := P2PKWitness{
			Signatures: []string{hex.EncodeToString(signatureBytes)},
		}

		witness, err := json.Marshal(p2pkWitness)
		if err != nil {
			return nil, err
		}
		output.Witness = string(witness)
		outputs[i] = output
	}

	return outputs, nil
}

func IsSigAll(secret nut10.WellKnownSecret) bool {
	for _, tag := range secret.Tags {
		if len(tag) == 2 {
			if tag[0] == "sigflag" && tag[1] == "SIG_ALL" {
				return true
			}
		}
	}

	return false
}

func CanSign(secret nut10.WellKnownSecret, key *btcec.PrivateKey) bool {
	secretData, err := hex.DecodeString(secret.Data)
	if err != nil {
		return false
	}

	publicKey, err := secp256k1.ParsePubKey(secretData)
	if err != nil {
		return false
	}

	if reflect.DeepEqual(publicKey.SerializeCompressed(), key.PubKey().SerializeCompressed()) {
		return true
	}

	return false
}
