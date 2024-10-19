package nut14

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/schnorr"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
	"github.com/elnosh/gonuts/cashu/nuts/nut11"
)

type HTLCWitness struct {
	Preimage   string   `json:"preimage"`
	Signatures []string `json:"signatures"`
}

func HTLCSecret(hash string, p2pkTags nut11.P2PKTags) (string, error) {
	// generate random nonce
	nonceBytes := make([]byte, 32)
	_, err := rand.Read(nonceBytes)
	if err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(nonceBytes)

	var tags [][]string
	if len(p2pkTags.Sigflag) > 0 {
		tags = append(tags, []string{nut11.SIGFLAG, p2pkTags.Sigflag})
	}
	if p2pkTags.NSigs > 0 {
		numStr := strconv.Itoa(p2pkTags.NSigs)
		tags = append(tags, []string{nut11.NSIGS, numStr})
	}
	if len(p2pkTags.Pubkeys) > 0 {
		pubkeys := []string{nut11.PUBKEYS}
		for _, pubkey := range p2pkTags.Pubkeys {
			key := hex.EncodeToString(pubkey.SerializeCompressed())
			pubkeys = append(pubkeys, key)
		}
		tags = append(tags, pubkeys)
	}
	if p2pkTags.Locktime > 0 {
		locktime := strconv.Itoa(int(p2pkTags.Locktime))
		tags = append(tags, []string{nut11.LOCKTIME, locktime})
	}
	if len(p2pkTags.Refund) > 0 {
		refundKeys := []string{nut11.REFUND}
		for _, pubkey := range p2pkTags.Refund {
			key := hex.EncodeToString(pubkey.SerializeCompressed())
			refundKeys = append(refundKeys, key)
		}
		tags = append(tags, refundKeys)
	}

	secretData := nut10.WellKnownSecret{
		Nonce: nonce,
		Data:  hash,
		Tags:  tags,
	}

	secret, err := nut10.SerializeSecret(nut10.HTLC, secretData)
	if err != nil {
		return "", err
	}

	return secret, nil
}

func IsSecretHTLC(proof cashu.Proof) bool {
	return nut10.SecretType(proof) == nut10.HTLC
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

		p2pkWitness := HTLCWitness{
			Preimage:   preimage,
			Signatures: []string{hex.EncodeToString(signatureBytes)},
		}

		witness, err := json.Marshal(p2pkWitness)
		if err != nil {
			return nil, err
		}
		proof.Witness = string(witness)
		proofs[i] = proof
	}

	return proofs, nil
}
