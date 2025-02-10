package nut20

import (
	"crypto/sha256"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
)

func SignMintQuote(
	privateKey *secp256k1.PrivateKey,
	quoteId string,
	blindedMessages cashu.BlindedMessages,
) (*schnorr.Signature, error) {
	msg := quoteId
	for _, bm := range blindedMessages {
		msg += bm.B_
	}

	hash := sha256.Sum256([]byte(msg))
	sig, err := schnorr.Sign(privateKey, hash[:])
	if err != nil {
		return nil, err
	}

	return sig, nil
}

func VerifyMintQuoteSignature(
	signature *schnorr.Signature,
	quoteId string,
	blindedMessages cashu.BlindedMessages,
	publicKey *secp256k1.PublicKey,
) bool {
	msg := quoteId
	for _, bm := range blindedMessages {
		msg += bm.B_
	}
	hash := sha256.Sum256([]byte(msg))

	return signature.Verify(hash[:], publicKey)
}
