package nut12

import (
	"encoding/hex"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
)

func TestVerifyBlindSiagnatureDLEQ(t *testing.T) {
	Ahex, _ := hex.DecodeString("0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	A, _ := secp256k1.ParsePubKey(Ahex)
	B_ := "02a9acc1e48c25eeeb9289b5031cc57da9fe72f3fe2861d264bdc074209b107ba2"
	C_ := "02a9acc1e48c25eeeb9289b5031cc57da9fe72f3fe2861d264bdc074209b107ba2"

	dleq := cashu.DLEQProof{
		E: "9818e061ee51d5c8edc3342369a554998ff7b4381c8652d724cdf46429be73d9",
		S: "9818e061ee51d5c8edc3342369a554998ff7b4381c8652d724cdf46429be73da",
	}

	if !VerifyBlindSignatureDLEQ(dleq, A, B_, C_) {
		t.Errorf("DLEQ verification on blind signature failed")
	}

}

func TestVerifyProofDLEQ(t *testing.T) {
	Ahex, _ := hex.DecodeString("0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	A, _ := secp256k1.ParsePubKey(Ahex)

	proof := cashu.Proof{
		Amount: 1,
		Id:     "00882760bfa2eb41",
		Secret: "daf4dd00a2b68a0858a80450f52c8a7d2ccf87d375e43e216e0c571f089f63e9",
		C:      "024369d2d22a80ecf78f3937da9d5f30c1b9f74f0c32684d583cca0fa6a61cdcfc",
		DLEQ: &cashu.DLEQProof{
			E: "b31e58ac6527f34975ffab13e70a48b6d2b0d35abc4b03f0151f09ee1a9763d4",
			S: "8fbae004c59e754d71df67e392b6ae4e29293113ddc2ec86592a0431d16306d8",
			R: "a6d13fcd7a18442e6076f5e1e7c887ad5de40a019824bdfa9fe740d302e8d861",
		},
	}

	if !VerifyProofDLEQ(proof, A) {
		t.Errorf("DLEQ verification on proof failed")
	}
}
