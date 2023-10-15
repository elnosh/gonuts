package crypto

import (
	"crypto/sha256"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func HashToCurve(message []byte) *secp256k1.PublicKey {
	var point *secp256k1.PublicKey

	for point == nil || !point.IsOnCurve() {
		hash := sha256.Sum256(message)
		pkhash := append([]byte{0x02}, hash[:]...)
		point, _ = secp256k1.ParsePubKey(pkhash)
		message = hash[:]
	}
	return point
}

// B_ = Y + rG
func BlindMessage(secret []byte, blindingFactor []byte) (*secp256k1.PublicKey, *secp256k1.PrivateKey) {
	var ypoint, rpoint, blindedMessage secp256k1.JacobianPoint

	Y := HashToCurve(secret)
	Y.AsJacobian(&ypoint)

	r, rpub := btcec.PrivKeyFromBytes(blindingFactor)
	rpub.AsJacobian(&rpoint)

	// blindedMessage = Y + rG (rpub)
	secp256k1.AddNonConst(&ypoint, &rpoint, &blindedMessage)
	blindedMessage.ToAffine()
	B_ := secp256k1.NewPublicKey(&blindedMessage.X, &blindedMessage.Y)

	return B_, r
}

// C_ = kB_
func SignBlindedMessage(B_ *secp256k1.PublicKey, k *secp256k1.PrivateKey) *secp256k1.PublicKey {
	var bpoint, result secp256k1.JacobianPoint
	B_.AsJacobian(&bpoint)

	// result = k * B_
	secp256k1.ScalarMultNonConst(&k.Key, &bpoint, &result)
	result.ToAffine()
	C_ := secp256k1.NewPublicKey(&result.X, &result.Y)

	return C_
}

// C = C_ - rK
func UnblindSignature(C_ *secp256k1.PublicKey, r *secp256k1.PrivateKey,
	K *secp256k1.PublicKey) *secp256k1.PublicKey {

	var Kpoint, rKPoint, CPoint secp256k1.JacobianPoint
	K.AsJacobian(&Kpoint)

	var rNeg secp256k1.ModNScalar
	rNeg.NegateVal(&r.Key)

	secp256k1.ScalarMultNonConst(&rNeg, &Kpoint, &rKPoint)

	var C_Point secp256k1.JacobianPoint
	C_.AsJacobian(&C_Point)
	secp256k1.AddNonConst(&C_Point, &rKPoint, &CPoint)
	CPoint.ToAffine()

	C := secp256k1.NewPublicKey(&CPoint.X, &CPoint.Y)
	return C
}

// k * HashToCurve(secret) == C
func Verify(secret []byte, k *secp256k1.PrivateKey, C *secp256k1.PublicKey) bool {
	var Ypoint, result secp256k1.JacobianPoint
	Y := HashToCurve(secret)
	Y.AsJacobian(&Ypoint)

	secp256k1.ScalarMultNonConst(&k.Key, &Ypoint, &result)
	result.ToAffine()
	pk := secp256k1.NewPublicKey(&result.X, &result.Y)

	return C.IsEqual(pk)
}
