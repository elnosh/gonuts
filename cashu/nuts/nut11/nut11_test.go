package nut11

import (
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
)

func TestIsSigAll(t *testing.T) {
	tests := []struct {
		p2pkSecretData nut10.WellKnownSecret
		expected       bool
	}{
		{
			p2pkSecretData: nut10.WellKnownSecret{
				Data: nut10.SecretData{
					Tags: [][]string{},
				},
			},
			expected: false,
		},
		{
			p2pkSecretData: nut10.WellKnownSecret{
				Data: nut10.SecretData{
					Tags: [][]string{{"sigflag", "SIG_INPUTS"}},
				},
			},
			expected: false,
		},
		{
			p2pkSecretData: nut10.WellKnownSecret{
				Data: nut10.SecretData{
					Tags: [][]string{
						{"locktime", "882912379"},
						{"refund", "refundkey"},
						{"sigflag", "SIG_ALL"},
					},
				},
			},
			expected: true,
		},
	}

	for _, test := range tests {
		result := IsSigAll(test.p2pkSecretData)
		if result != test.expected {
			t.Fatalf("expected '%v' but got '%v' instead", test.expected, result)
		}
	}
}

func TestCanSign(t *testing.T) {
	privateKey, _ := btcec.NewPrivateKey()
	publicKey := hex.EncodeToString(privateKey.PubKey().SerializeCompressed())

	tests := []struct {
		p2pkSecretData nut10.WellKnownSecret
		expected       bool
	}{
		{
			p2pkSecretData: nut10.WellKnownSecret{
				Data: nut10.SecretData{
					Data: publicKey,
				},
			},
			expected: true,
		},

		{
			p2pkSecretData: nut10.WellKnownSecret{
				Data: nut10.SecretData{
					Data: "somerandomkey",
				},
			},
			expected: false,
		},

		{
			p2pkSecretData: nut10.WellKnownSecret{
				Data: nut10.SecretData{
					Data: "sdjflksjdflsdjfd",
				},
			},
			expected: false,
		},
	}

	for _, test := range tests {
		result := CanSign(test.p2pkSecretData, privateKey)
		if result != test.expected {
			t.Fatalf("expected '%v' but got '%v' instead", test.expected, result)
		}
	}
}
