package nut14

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/schnorr"
	"github.com/elnosh/gonuts/cashu"
)

type HTLCWitness struct {
	Preimage   string   `json:"preimage"`
	Signatures []string `json:"signatures"`
}

func AddWitnessHTLC(
	proofs cashu.Proofs,
	preimage string,
	signingKey *btcec.PrivateKey,
) (cashu.Proofs, error) {
	for i, proof := range proofs {
		hash := sha256.Sum256([]byte(proof.Secret))
		signature, err := schnorr.Sign(signingKey, hash[:])
		if err != nil {
			return nil, err
		}
		signatureBytes := signature.Serialize()

		htlcWitness := HTLCWitness{
			Preimage:   preimage,
			Signatures: []string{hex.EncodeToString(signatureBytes)},
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
