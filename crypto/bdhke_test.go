package crypto

import (
	"encoding/hex"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func TestHashToCurve(t *testing.T) {
	tests := []struct {
		message  string
		expected string
	}{

		{message: "0000000000000000000000000000000000000000000000000000000000000000",
			expected: "024cce997d3b518f739663b757deaec95bcd9473c30a14ac2fd04023a739d1a725"},
		{message: "0000000000000000000000000000000000000000000000000000000000000001",
			expected: "022e7158e11c9506f1aa4248bf531298daa7febd6194f003edcd9b93ade6253acf"},
	}

	for _, test := range tests {
		msgBytes, err := hex.DecodeString(test.message)
		if err != nil {
			t.Errorf("error decoding msg: %v", err)
		}

		pk, err := HashToCurve(msgBytes)
		if err != nil {
			t.Fatalf("HashToCurve err: %v", err)
		}

		hexStr := hex.EncodeToString(pk.SerializeCompressed())
		if hexStr != test.expected {
			t.Errorf("expected '%v' but got '%v' instead\n", test.expected, hexStr)
		}
	}
}

func TestBlindMessage(t *testing.T) {
	tests := []struct {
		secret         string
		blindingFactor string
		expected       string
	}{
		{secret: "test_message",
			blindingFactor: "0000000000000000000000000000000000000000000000000000000000000001",
			expected:       "025cc16fe33b953e2ace39653efb3e7a7049711ae1d8a2f7a9108753f1cdea742b",
		},
	}

	for _, test := range tests {
		rbytes, err := hex.DecodeString(test.blindingFactor)
		if err != nil {
			t.Errorf("error decoding blinding factor: %v", err)
		}
		r := secp256k1.PrivKeyFromBytes(rbytes)

		B_, _, _ := BlindMessage(test.secret, r)
		B_Hex := hex.EncodeToString(B_.SerializeCompressed())
		if B_Hex != test.expected {
			t.Errorf("expected '%v' but got '%v' instead\n", test.expected, B_Hex)
		}
	}
}

func TestSignBlindedMessage(t *testing.T) {
	tests := []struct {
		secret         string
		blindingFactor string
		mintPrivKey    string
		expected       string
	}{
		{secret: "test_message",
			blindingFactor: "0000000000000000000000000000000000000000000000000000000000000001",
			mintPrivKey:    "0000000000000000000000000000000000000000000000000000000000000001",
			expected:       "025cc16fe33b953e2ace39653efb3e7a7049711ae1d8a2f7a9108753f1cdea742b",
		},
	}

	for _, test := range tests {
		rbytes, err := hex.DecodeString(test.blindingFactor)
		if err != nil {
			t.Errorf("error decoding blinding factor: %v", err)
		}
		r := secp256k1.PrivKeyFromBytes(rbytes)

		B_, _, _ := BlindMessage(test.secret, r)

		mintKeyBytes, err := hex.DecodeString(test.mintPrivKey)
		if err != nil {
			t.Errorf("error decoding mint private key: %v", err)
		}

		k := secp256k1.PrivKeyFromBytes(mintKeyBytes)

		blindedSignature := SignBlindedMessage(B_, k)
		blindedHex := hex.EncodeToString(blindedSignature.SerializeCompressed())
		if blindedHex != test.expected {
			t.Errorf("expected '%v' but got '%v' instead\n", test.expected, blindedHex)
		}
	}
}

func TestUnblindSignature(t *testing.T) {
	tests := []struct {
		C_str    string
		kstr     string
		rstr     string
		expected string
	}{
		{
			C_str:    "02a9acc1e48c25eeeb9289b5031cc57da9fe72f3fe2861d264bdc074209b107ba2",
			kstr:     "020000000000000000000000000000000000000000000000000000000000000001",
			rstr:     "0000000000000000000000000000000000000000000000000000000000000001",
			expected: "03c724d7e6a5443b39ac8acf11f40420adc4f99a02e7cc1b57703d9391f6d129cd",
		},
		{
			C_str:    "025cc16fe33b953e2ace39653efb3e7a7049711ae1d8a2f7a9108753f1cdea742b",
			kstr:     "020000000000000000000000000000000000000000000000000000000000000001",
			rstr:     "0000000000000000000000000000000000000000000000000000000000000001",
			expected: "0271bf0d702dbad86cbe0af3ab2bfba70a0338f22728e412d88a830ed0580b9de4",
		},
	}

	for _, test := range tests {
		dst, _ := hex.DecodeString(test.C_str)
		C_, err := secp256k1.ParsePubKey(dst)
		if err != nil {
			t.Error(err)
		}

		kdst, _ := hex.DecodeString(test.kstr)
		K, err := secp256k1.ParsePubKey(kdst)
		if err != nil {
			t.Error(err)
		}

		rhex, _ := hex.DecodeString(test.rstr)
		r := secp256k1.PrivKeyFromBytes(rhex)

		C := UnblindSignature(C_, r, K)
		CHex := hex.EncodeToString(C.SerializeCompressed())
		if CHex != test.expected {
			t.Errorf("expected '%v' but got '%v' instead\n", test.expected, CHex)
		}

	}
}

func TestVerify(t *testing.T) {
	secret := "test_message"
	rhex, _ := hex.DecodeString("0000000000000000000000000000000000000000000000000000000000000002")
	r := secp256k1.PrivKeyFromBytes(rhex)

	B_, r, _ := BlindMessage(secret, r)

	khex, _ := hex.DecodeString("0000000000000000000000000000000000000000000000000000000000000001")
	k := secp256k1.PrivKeyFromBytes(khex)
	K := k.PubKey()

	C_ := SignBlindedMessage(B_, k)
	C := UnblindSignature(C_, r, K)

	if !Verify(secret, k, C) {
		t.Error("failed verification")
	}
}
