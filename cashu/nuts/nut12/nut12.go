package nut12

import (
	"encoding/hex"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
)

// VerifyProofsDLEQ will verify the DLEQ proofs if present. If the DLEQ proofs are not present
// it will continue and return true
func VerifyProofsDLEQ(proofs cashu.Proofs, keyset crypto.WalletKeyset) bool {
	for _, proof := range proofs {
		if proof.DLEQ == nil {
			continue
		} else {
			pubkey, ok := keyset.PublicKeys[proof.Amount]
			if !ok {
				return false
			}

			if !VerifyProofDLEQ(proof, pubkey) {
				return false
			}
		}
	}
	return true
}

func VerifyProofDLEQ(
	proof cashu.Proof,
	A *secp256k1.PublicKey,
) bool {
	e, s, r, err := ParseDLEQ(*proof.DLEQ)
	if err != nil || r == nil {
		return false
	}

	B_, _, err := crypto.BlindMessage(proof.Secret, r)
	if err != nil {
		return false
	}

	CBytes, err := hex.DecodeString(proof.C)
	if err != nil {
		return false
	}

	C, err := secp256k1.ParsePubKey(CBytes)
	if err != nil {
		return false
	}

	var CPoint, APoint secp256k1.JacobianPoint
	C.AsJacobian(&CPoint)
	A.AsJacobian(&APoint)

	// C' = C + r*A
	var C_Point, rAPoint secp256k1.JacobianPoint
	secp256k1.ScalarMultNonConst(&r.Key, &APoint, &rAPoint)
	rAPoint.ToAffine()
	secp256k1.AddNonConst(&CPoint, &rAPoint, &C_Point)
	C_Point.ToAffine()
	C_ := secp256k1.NewPublicKey(&C_Point.X, &C_Point.Y)

	return crypto.VerifyDLEQ(e, s, A, B_, C_)
}

func VerifyBlindSignatureDLEQ(
	dleq cashu.DLEQProof,
	A *secp256k1.PublicKey,
	B_str string,
	C_str string,
) bool {
	e, s, _, err := ParseDLEQ(dleq)
	if err != nil {
		return false
	}

	B_bytes, err := hex.DecodeString(B_str)
	if err != nil {
		return false
	}
	B_, err := secp256k1.ParsePubKey(B_bytes)
	if err != nil {
		return false
	}

	C_bytes, err := hex.DecodeString(C_str)
	if err != nil {
		return false
	}
	C_, err := secp256k1.ParsePubKey(C_bytes)
	if err != nil {
		return false
	}

	return crypto.VerifyDLEQ(e, s, A, B_, C_)
}

func ParseDLEQ(dleq cashu.DLEQProof) (
	*secp256k1.PrivateKey,
	*secp256k1.PrivateKey,
	*secp256k1.PrivateKey,
	error,
) {
	ebytes, err := hex.DecodeString(dleq.E)
	if err != nil {
		return nil, nil, nil, err
	}
	e := secp256k1.PrivKeyFromBytes(ebytes)

	sbytes, err := hex.DecodeString(dleq.S)
	if err != nil {
		return nil, nil, nil, err
	}
	s := secp256k1.PrivKeyFromBytes(sbytes)

	if dleq.R == "" {
		return e, s, nil, nil
	}

	rbytes, err := hex.DecodeString(dleq.R)
	if err != nil {
		return nil, nil, nil, err
	}
	r := secp256k1.PrivKeyFromBytes(rbytes)

	return e, s, r, nil
}
