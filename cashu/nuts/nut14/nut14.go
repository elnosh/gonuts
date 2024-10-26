package nut14

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
	"github.com/elnosh/gonuts/cashu/nuts/nut11"
)

const (
	NUT14ErrCode cashu.CashuErrCode = 30004
)

// NUT-14 specific errors
var (
	InvalidPreimageErr = cashu.Error{Detail: "Invalid preimage for HTLC", Code: NUT14ErrCode}
	InvalidHashErr     = cashu.Error{Detail: "Invalid hash in secret", Code: NUT14ErrCode}
)

type HTLCWitness struct {
	Preimage   string   `json:"preimage"`
	Signatures []string `json:"signatures"`
}

// AddWitnessHTLC will add the preimage to the HTLCWitness.
// It will also read the tags in the secret and add the signatures
// if needed.
func AddWitnessHTLC(
	proofs cashu.Proofs,
	secret nut10.WellKnownSecret,
	preimage string,
	signingKey *btcec.PrivateKey,
) (cashu.Proofs, error) {
	tags, err := nut11.ParseP2PKTags(secret.Data.Tags)
	if err != nil {
		return nil, err
	}

	signatureNeeded := false
	if tags.NSigs > 0 {
		// return error if it requires more than 1 signature
		if tags.NSigs > 1 {
			return nil, errors.New("unable to provide enough signatures")
		}

		publicKey := signingKey.PubKey().SerializeCompressed()
		canSign := false
		// read pubkeys and check signingKey can sign
		for _, pk := range tags.Pubkeys {
			if slices.Equal(pk.SerializeCompressed(), publicKey) {
				canSign = true
				break
			}
		}
		if !canSign {
			return nil, errors.New("signing key is not part of public keys list that can provide signatures")
		}

		// if it gets to here, signature is needed in the witness
		signatureNeeded = true
	}

	for i, proof := range proofs {
		htlcWitness := HTLCWitness{Preimage: preimage}
		if signatureNeeded {
			hash := sha256.Sum256([]byte(proof.Secret))
			signature, err := schnorr.Sign(signingKey, hash[:])
			if err != nil {
				return nil, err
			}
			htlcWitness.Signatures = []string{hex.EncodeToString(signature.Serialize())}
		}

		witness, err := json.Marshal(htlcWitness)
		if err != nil {
			return nil, err
		}
		proof.Witness = string(witness)
		proofs[i] = proof
	}

	return proofs, nil
}

func AddWitnessHTLCToOutputs(
	outputs cashu.BlindedMessages,
	preimage string,
	signingKey *btcec.PrivateKey,
) (cashu.BlindedMessages, error) {
	for i, output := range outputs {
		hash := sha256.Sum256([]byte(output.B_))
		signature, err := schnorr.Sign(signingKey, hash[:])
		if err != nil {
			return nil, err
		}

		htlcWitness := HTLCWitness{
			Preimage:   preimage,
			Signatures: []string{hex.EncodeToString(signature.Serialize())},
		}

		witness, err := json.Marshal(htlcWitness)
		if err != nil {
			return nil, err
		}
		output.Witness = string(witness)
		outputs[i] = output
	}

	return outputs, nil
}
