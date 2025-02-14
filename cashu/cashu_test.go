package cashu

import (
	"encoding/hex"
	"math"
	"math/big"
	"reflect"
	"testing"
)

func TestAmountChecked(t *testing.T) {
	split := AmountSplit(math.MaxUint64)
	overflowBlindedMessages := make(BlindedMessages, len(split)+1)
	for i, amount := range split {
		overflowBlindedMessages[i] = BlindedMessage{Amount: amount}
	}
	overflowBlindedMessages[len(split)] = BlindedMessage{Amount: 4}

	tests := []struct {
		blindedMessages BlindedMessages
		expectedAmount  uint64
		expectedErr     error
	}{
		{
			blindedMessages: BlindedMessages{
				BlindedMessage{Amount: 2},
				BlindedMessage{Amount: 4},
				BlindedMessage{Amount: 8},
				BlindedMessage{Amount: 64},
			},
			expectedAmount: 78,
			expectedErr:    nil,
		},
		{
			blindedMessages: overflowBlindedMessages,
			expectedAmount:  0,
			expectedErr:     ErrAmountOverflows,
		},
	}

	for _, test := range tests {
		totalAmount, err := test.blindedMessages.AmountChecked()
		if totalAmount != test.expectedAmount {
			t.Fatalf("expected total amount of '%v' but got '%v'", test.expectedAmount, totalAmount)
		}

		if err != test.expectedErr {
			t.Fatalf("expected error '%v' but got '%v'", test.expectedErr, err)
		}
	}
}

func TestOverflowAddUint64(t *testing.T) {
	tests := []struct {
		a                uint64
		b                uint64
		expectedUint64   uint64
		expectedOverflow bool
	}{
		{
			a:                21,
			b:                42,
			expectedUint64:   63,
			expectedOverflow: false,
		},
		{
			a:                math.MaxUint64 - 5,
			b:                10,
			expectedUint64:   math.MaxUint64,
			expectedOverflow: true,
		},
	}

	for _, test := range tests {
		result, overflow := OverflowAddUint64(test.a, test.b)
		if result != test.expectedUint64 {
			t.Fatalf("expected result '%v' but got '%v'", test.expectedUint64, result)
		}

		if overflow != test.expectedOverflow {
			t.Fatalf("expected overflow '%v' but got '%v'", test.expectedOverflow, overflow)
		}
	}
}

func FuzzOverflowAddUint64(f *testing.F) {
	cases := [][2]uint64{
		{21, 42},
		{math.MaxUint64, 10},
	}
	for _, seed := range cases {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, a uint64, b uint64) {
		bigA := new(big.Int).SetUint64(a)
		bigB := new(big.Int).SetUint64(b)
		bigA.Add(bigA, bigB)

		result, overflow := OverflowAddUint64(a, b)
		// IsUint64 reports whether the number can be represented as uint64
		if bigA.IsUint64() {
			uint64Result := bigA.Uint64()
			if uint64Result != result {
				t.Errorf("a = %v and b = %v. expected result %v but got %v", a, b, uint64Result, result)
			}
		} else {
			// if result from addition cannot be represented as uint64,
			// then function should return overflow == true
			if !overflow {
				t.Error("addition is above max uint64 but did not return overflow")
			}
		}
	})
}

func TestUnderflowSubUint64(t *testing.T) {
	tests := []struct {
		a                 uint64
		b                 uint64
		expectedUint64    uint64
		expectedUnderflow bool
	}{
		{
			a:                 42,
			b:                 21,
			expectedUint64:    21,
			expectedUnderflow: false,
		},
		{
			a:                 10,
			b:                 210,
			expectedUint64:    0,
			expectedUnderflow: true,
		},
	}

	for _, test := range tests {
		result, underflow := UnderflowSubUint64(test.a, test.b)
		if result != test.expectedUint64 {
			t.Fatalf("expected result '%v' but got '%v'", test.expectedUint64, result)
		}

		if underflow != test.expectedUnderflow {
			t.Fatalf("expected overflow '%v' but got '%v'", test.expectedUnderflow, underflow)
		}
	}
}

func FuzzUnderflowSubUint64(f *testing.F) {
	cases := [][2]uint64{
		{42, 21},
		{10, 210},
	}
	for _, seed := range cases {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, a uint64, b uint64) {
		bigA := new(big.Int).SetUint64(a)
		bigB := new(big.Int).SetUint64(b)
		bigA.Sub(bigA, bigB)

		result, underflow := UnderflowSubUint64(a, b)
		// IsUint64 reports whether the number can be represented as uint64
		if bigA.IsUint64() {
			uint64Result := bigA.Uint64()
			if uint64Result != result {
				t.Errorf("a = %v and b = %v. expected result %v but got %v", a, b, uint64Result, result)
			}
		} else {
			// if result from sub cannot be represented as uint64,
			// then function should return underflow == true
			if !underflow {
				t.Error("subtraction is below 0 but did not return underflow")
			}
		}
	})
}

func TestDecodeTokenV4(t *testing.T) {
	keysetIdBytes, _ := hex.DecodeString("00ad268c4d1f5826")
	Cbytes, _ := hex.DecodeString("038618543ffb6b8695df4ad4babcde92a34a96bdcd97dcee0d7ccf98d472126792")
	keysetId2Bytes, _ := hex.DecodeString("00ffd48b8f5ecf80")
	C2Bytes, _ := hex.DecodeString("0244538319de485d55bed3b29a642bee5879375ab9e7a620e11e48ba482421f3cf")
	C3Bytes, _ := hex.DecodeString("023456aa110d84b4ac747aebd82c3b005aca50bf457ebd5737a4414fac3ae7d94d")
	C4Bytes, _ := hex.DecodeString("0273129c5719e599379a974a626363c333c56cafc0e6d01abe46d5808280789c63")

	tests := []struct {
		tokenString string
		expected    TokenV4
	}{
		{
			tokenString: "cashuBpGF0gaJhaUgArSaMTR9YJmFwgaNhYQFhc3hAOWE2ZGJiODQ3YmQyMzJiYTc2ZGIwZGYxOTcyMTZiMjlkM2I4Y2MxNDU1M2NkMjc4MjdmYzFjYzk0MmZlZGI0ZWFjWCEDhhhUP_trhpXfStS6vN6So0qWvc2X3O4NfM-Y1HISZ5JhZGlUaGFuayB5b3VhbXVodHRwOi8vbG9jYWxob3N0OjMzMzhhdWNzYXQ=",
			expected: TokenV4{
				MintURL: "http://localhost:3338",
				TokenProofs: []TokenV4Proof{
					{
						Id: keysetIdBytes,
						Proofs: []ProofV4{
							{
								Amount: 1,
								Secret: "9a6dbb847bd232ba76db0df197216b29d3b8cc14553cd27827fc1cc942fedb4e",
								C:      Cbytes,
							},
						},
					},
				},
				Unit: "sat",
				Memo: "Thank you",
			},
		},
		{
			tokenString: "cashuBo2F0gqJhaUgA_9SLj17PgGFwgaNhYQFhc3hAYWNjMTI0MzVlN2I4NDg0YzNjZjE4NTAxNDkyMThhZjkwZjcxNmE1MmJmNGE1ZWQzNDdlNDhlY2MxM2Y3NzM4OGFjWCECRFODGd5IXVW-07KaZCvuWHk3WrnnpiDhHki6SCQh88-iYWlIAK0mjE0fWCZhcIKjYWECYXN4QDEzMjNkM2Q0NzA3YTU4YWQyZTIzYWRhNGU5ZjFmNDlmNWE1YjRhYzdiNzA4ZWIwZDYxZjczOGY0ODMwN2U4ZWVhY1ghAjRWqhENhLSsdHrr2Cw7AFrKUL9Ffr1XN6RBT6w659lNo2FhAWFzeEA1NmJjYmNiYjdjYzY0MDZiM2ZhNWQ1N2QyMTc0ZjRlZmY4YjQ0MDJiMTc2OTI2ZDNhNTdkM2MzZGNiYjU5ZDU3YWNYIQJzEpxXGeWZN5qXSmJjY8MzxWyvwObQGr5G1YCCgHicY2FtdWh0dHA6Ly9sb2NhbGhvc3Q6MzMzOGF1Y3NhdA",
			expected: TokenV4{
				MintURL: "http://localhost:3338",
				TokenProofs: []TokenV4Proof{
					{
						Id: keysetId2Bytes,
						Proofs: []ProofV4{
							{
								Amount: 1,
								Secret: "acc12435e7b8484c3cf1850149218af90f716a52bf4a5ed347e48ecc13f77388",
								C:      C2Bytes,
							},
						},
					},
					{
						Id: keysetIdBytes,
						Proofs: []ProofV4{
							{
								Amount: 2,
								Secret: "1323d3d4707a58ad2e23ada4e9f1f49f5a5b4ac7b708eb0d61f738f48307e8ee",
								C:      C3Bytes,
							},
							{
								Amount: 1,
								Secret: "56bcbcbb7cc6406b3fa5d57d2174f4eff8b4402b176926d3a57d3c3dcbb59d57",
								C:      C4Bytes,
							},
						},
					},
				},
				Unit: "sat",
			},
		},
	}

	for _, test := range tests {
		token, _ := DecodeTokenV4(test.tokenString)
		if token.Unit != test.expected.Unit {
			t.Errorf("expected '%v' but got '%v' instead", test.expected.Unit, token.Unit)
		}

		if token.Memo != test.expected.Memo {
			t.Errorf("expected '%v' but got '%v' instead", test.expected.Memo, token.Memo)
		}

		if token.Mint() != test.expected.MintURL {
			t.Errorf("expected '%v' but got '%v' instead", test.expected.MintURL, token.Mint())
		}

		proofs := token.Proofs()
		expectedProofs := test.expected.Proofs()
		for i, proof := range proofs {
			if proof.Id != expectedProofs[i].Id {
				t.Errorf("expected '%v' but got '%v' instead", expectedProofs[i].Id, proof.Id)
			}

			if proof.Amount != expectedProofs[i].Amount {
				t.Errorf("expected '%v' but got '%v' instead", test.expected.TokenProofs[0].Proofs[i].Amount, proof.Amount)
			}

			if proof.Secret != expectedProofs[i].Secret {
				t.Errorf("expected '%v' but got '%v' instead", test.expected.TokenProofs[0].Proofs[i].Secret, proof.Secret)
			}

			if proof.C != expectedProofs[i].C {
				t.Errorf("expected '%v' but got '%v' instead", expectedProofs[i].C, proof.C)
			}
		}
	}
}

func TestSerializeTokenV4(t *testing.T) {
	keysetBytes, _ := hex.DecodeString("00ad268c4d1f5826")
	C, _ := hex.DecodeString("038618543ffb6b8695df4ad4babcde92a34a96bdcd97dcee0d7ccf98d472126792")

	keysetId2Bytes, _ := hex.DecodeString("00ffd48b8f5ecf80")
	C2Bytes, _ := hex.DecodeString("0244538319de485d55bed3b29a642bee5879375ab9e7a620e11e48ba482421f3cf")
	C3Bytes, _ := hex.DecodeString("023456aa110d84b4ac747aebd82c3b005aca50bf457ebd5737a4414fac3ae7d94d")
	C4Bytes, _ := hex.DecodeString("0273129c5719e599379a974a626363c333c56cafc0e6d01abe46d5808280789c63")

	tests := []struct {
		token    TokenV4
		expected string
	}{
		{
			token: TokenV4{
				TokenProofs: []TokenV4Proof{
					{
						Id: keysetBytes,
						Proofs: []ProofV4{
							{
								Amount: 1,
								Secret: "9a6dbb847bd232ba76db0df197216b29d3b8cc14553cd27827fc1cc942fedb4e",
								C:      C,
							},
						},
					},
				},
				Memo:    "Thank you",
				MintURL: "http://localhost:3338",
				Unit:    "sat",
			},
			expected: "cashuBpGF0gaJhaUgArSaMTR9YJmFwgaNhYQFhc3hAOWE2ZGJiODQ3YmQyMzJiYTc2ZGIwZGYxOTcyMTZiMjlkM2I4Y2MxNDU1M2NkMjc4MjdmYzFjYzk0MmZlZGI0ZWFjWCEDhhhUP_trhpXfStS6vN6So0qWvc2X3O4NfM-Y1HISZ5JhZGlUaGFuayB5b3VhbXVodHRwOi8vbG9jYWxob3N0OjMzMzhhdWNzYXQ",
		},
		{
			token: TokenV4{
				MintURL: "http://localhost:3338",
				Unit:    "sat",
				TokenProofs: []TokenV4Proof{
					{
						Id: keysetId2Bytes,
						Proofs: []ProofV4{
							{
								Amount: 1,
								Secret: "acc12435e7b8484c3cf1850149218af90f716a52bf4a5ed347e48ecc13f77388",
								C:      C2Bytes,
							},
						},
					},
					{
						Id: keysetBytes,
						Proofs: []ProofV4{
							{
								Amount: 2,
								Secret: "1323d3d4707a58ad2e23ada4e9f1f49f5a5b4ac7b708eb0d61f738f48307e8ee",
								C:      C3Bytes,
							},
							{
								Amount: 1,
								Secret: "56bcbcbb7cc6406b3fa5d57d2174f4eff8b4402b176926d3a57d3c3dcbb59d57",
								C:      C4Bytes,
							},
						},
					},
				},
			},
			expected: "cashuBo2F0gqJhaUgA_9SLj17PgGFwgaNhYQFhc3hAYWNjMTI0MzVlN2I4NDg0YzNjZjE4NTAxNDkyMThhZjkwZjcxNmE1MmJmNGE1ZWQzNDdlNDhlY2MxM2Y3NzM4OGFjWCECRFODGd5IXVW-07KaZCvuWHk3WrnnpiDhHki6SCQh88-iYWlIAK0mjE0fWCZhcIKjYWECYXN4QDEzMjNkM2Q0NzA3YTU4YWQyZTIzYWRhNGU5ZjFmNDlmNWE1YjRhYzdiNzA4ZWIwZDYxZjczOGY0ODMwN2U4ZWVhY1ghAjRWqhENhLSsdHrr2Cw7AFrKUL9Ffr1XN6RBT6w659lNo2FhAWFzeEA1NmJjYmNiYjdjYzY0MDZiM2ZhNWQ1N2QyMTc0ZjRlZmY4YjQ0MDJiMTc2OTI2ZDNhNTdkM2MzZGNiYjU5ZDU3YWNYIQJzEpxXGeWZN5qXSmJjY8MzxWyvwObQGr5G1YCCgHicY2FtdWh0dHA6Ly9sb2NhbGhvc3Q6MzMzOGF1Y3NhdA",
		},
	}

	for _, test := range tests {
		tokenString, err := test.token.Serialize()
		if err != nil {
			t.Fatal(err)
		}

		if tokenString != test.expected {
			t.Errorf("expected '%v'\n\n but got '%v' instead", test.expected, tokenString)
		}
	}
}

func TestDecodeTokenV3(t *testing.T) {
	tests := []struct {
		tokenString      string
		tokenWithPadding string
		expected         TokenV3
	}{
		{
			tokenString:      "cashuAeyJ0b2tlbiI6W3sibWludCI6Imh0dHBzOi8vODMzMy5zcGFjZTozMzM4IiwicHJvb2ZzIjpbeyJhbW91bnQiOjIsImlkIjoiMDA5YTFmMjkzMjUzZTQxZSIsInNlY3JldCI6IjQwNzkxNWJjMjEyYmU2MWE3N2UzZTZkMmFlYjRjNzI3OTgwYmRhNTFjZDA2YTZhZmMyOWUyODYxNzY4YTc4MzciLCJDIjoiMDJiYzkwOTc5OTdkODFhZmIyY2M3MzQ2YjVlNDM0NWE5MzQ2YmQyYTUwNmViNzk1ODU5OGE3MmYwY2Y4NTE2M2VhIn0seyJhbW91bnQiOjgsImlkIjoiMDA5YTFmMjkzMjUzZTQxZSIsInNlY3JldCI6ImZlMTUxMDkzMTRlNjFkNzc1NmIwZjhlZTBmMjNhNjI0YWNhYTNmNGUwNDJmNjE0MzNjNzI4YzcwNTdiOTMxYmUiLCJDIjoiMDI5ZThlNTA1MGI4OTBhN2Q2YzA5NjhkYjE2YmMxZDVkNWZhMDQwZWExZGUyODRmNmVjNjlkNjEyOTlmNjcxMDU5In1dfV0sInVuaXQiOiJzYXQiLCJtZW1vIjoiVGhhbmsgeW91IHZlcnkgbXVjaC4ifQ",
			tokenWithPadding: "cashuAeyJ0b2tlbiI6W3sibWludCI6Imh0dHBzOi8vODMzMy5zcGFjZTozMzM4IiwicHJvb2ZzIjpbeyJhbW91bnQiOjIsImlkIjoiMDA5YTFmMjkzMjUzZTQxZSIsInNlY3JldCI6IjQwNzkxNWJjMjEyYmU2MWE3N2UzZTZkMmFlYjRjNzI3OTgwYmRhNTFjZDA2YTZhZmMyOWUyODYxNzY4YTc4MzciLCJDIjoiMDJiYzkwOTc5OTdkODFhZmIyY2M3MzQ2YjVlNDM0NWE5MzQ2YmQyYTUwNmViNzk1ODU5OGE3MmYwY2Y4NTE2M2VhIn0seyJhbW91bnQiOjgsImlkIjoiMDA5YTFmMjkzMjUzZTQxZSIsInNlY3JldCI6ImZlMTUxMDkzMTRlNjFkNzc1NmIwZjhlZTBmMjNhNjI0YWNhYTNmNGUwNDJmNjE0MzNjNzI4YzcwNTdiOTMxYmUiLCJDIjoiMDI5ZThlNTA1MGI4OTBhN2Q2YzA5NjhkYjE2YmMxZDVkNWZhMDQwZWExZGUyODRmNmVjNjlkNjEyOTlmNjcxMDU5In1dfV0sInVuaXQiOiJzYXQiLCJtZW1vIjoiVGhhbmsgeW91IHZlcnkgbXVjaC4ifQ==",
			expected: TokenV3{
				Token: []TokenV3Proof{
					{
						Mint: "https://8333.space:3338",
						Proofs: Proofs{
							Proof{
								Amount: 2,
								Id:     "009a1f293253e41e",
								Secret: "407915bc212be61a77e3e6d2aeb4c727980bda51cd06a6afc29e2861768a7837",
								C:      "02bc9097997d81afb2cc7346b5e4345a9346bd2a506eb7958598a72f0cf85163ea",
							},
							Proof{
								Amount: 8,
								Id:     "009a1f293253e41e",
								Secret: "fe15109314e61d7756b0f8ee0f23a624acaa3f4e042f61433c728c7057b931be",
								C:      "029e8e5050b890a7d6c0968db16bc1d5d5fa040ea1de284f6ec69d61299f671059",
							},
						},
					},
				},
				Unit: "sat",
				Memo: "Thank you very much.",
			},
		},
	}

	for _, test := range tests {
		token, _ := DecodeTokenV3(test.tokenString)
		if token.Unit != test.expected.Unit {
			t.Errorf("expected '%v' but got '%v' instead", test.expected.Unit, token.Unit)
		}

		tokenPadding, _ := DecodeTokenV3(test.tokenWithPadding)
		if !reflect.DeepEqual(token, tokenPadding) {
			t.Error("decoded tokens do not match")
		}

		if token.Memo != test.expected.Memo {
			t.Errorf("expected '%v' but got '%v' instead", test.expected.Memo, token.Memo)
		}

		if token.Token[0].Mint != test.expected.Token[0].Mint {
			t.Errorf("expected '%v' but got '%v' instead", test.expected.Token[0].Mint, token.Token[0].Mint)
		}

		for i, proof := range token.Token[0].Proofs {
			if proof.Amount != test.expected.Token[0].Proofs[i].Amount {
				t.Errorf("expected '%v' but got '%v' instead", test.expected.Token[0].Proofs[i].Amount, proof.Amount)
			}

			if proof.Id != test.expected.Token[0].Proofs[i].Id {
				t.Errorf("expected '%v' but got '%v' instead", test.expected.Token[0].Proofs[i].Id, proof.Id)
			}

			if proof.Secret != test.expected.Token[0].Proofs[i].Secret {
				t.Errorf("expected '%v' but got '%v' instead", test.expected.Token[0].Proofs[i].Secret, proof.Secret)
			}

			if proof.C != test.expected.Token[0].Proofs[i].C {
				t.Errorf("expected '%v' but got '%v' instead", test.expected.Token[0].Proofs[i].C, proof.C)
			}
		}
	}
}

func TestSerializeTokenV3(t *testing.T) {
	tests := []struct {
		token    TokenV3
		expected string
	}{
		{
			token: TokenV3{
				Token: []TokenV3Proof{
					{
						Mint: "https://8333.space:3338",
						Proofs: Proofs{
							Proof{
								Amount: 2,
								Id:     "009a1f293253e41e",
								Secret: "407915bc212be61a77e3e6d2aeb4c727980bda51cd06a6afc29e2861768a7837",
								C:      "02bc9097997d81afb2cc7346b5e4345a9346bd2a506eb7958598a72f0cf85163ea",
							},
							Proof{
								Amount: 8,
								Id:     "009a1f293253e41e",
								Secret: "fe15109314e61d7756b0f8ee0f23a624acaa3f4e042f61433c728c7057b931be",
								C:      "029e8e5050b890a7d6c0968db16bc1d5d5fa040ea1de284f6ec69d61299f671059",
							},
						},
					},
				},
				Unit: "sat",
				Memo: "Thank you.",
			},

			expected: "cashuAeyJ0b2tlbiI6W3sibWludCI6Imh0dHBzOi8vODMzMy5zcGFjZTozMzM4IiwicHJvb2ZzIjpbeyJhbW91bnQiOjIsImlkIjoiMDA5YTFmMjkzMjUzZTQxZSIsInNlY3JldCI6IjQwNzkxNWJjMjEyYmU2MWE3N2UzZTZkMmFlYjRjNzI3OTgwYmRhNTFjZDA2YTZhZmMyOWUyODYxNzY4YTc4MzciLCJDIjoiMDJiYzkwOTc5OTdkODFhZmIyY2M3MzQ2YjVlNDM0NWE5MzQ2YmQyYTUwNmViNzk1ODU5OGE3MmYwY2Y4NTE2M2VhIn0seyJhbW91bnQiOjgsImlkIjoiMDA5YTFmMjkzMjUzZTQxZSIsInNlY3JldCI6ImZlMTUxMDkzMTRlNjFkNzc1NmIwZjhlZTBmMjNhNjI0YWNhYTNmNGUwNDJmNjE0MzNjNzI4YzcwNTdiOTMxYmUiLCJDIjoiMDI5ZThlNTA1MGI4OTBhN2Q2YzA5NjhkYjE2YmMxZDVkNWZhMDQwZWExZGUyODRmNmVjNjlkNjEyOTlmNjcxMDU5In1dfV0sInVuaXQiOiJzYXQiLCJtZW1vIjoiVGhhbmsgeW91LiJ9",
		},
	}

	for _, test := range tests {
		tokenString, err := test.token.Serialize()
		if err != nil {
			t.Fatal(err)
		}

		if tokenString != test.expected {
			t.Errorf("expected '%v'\n\n but got '%v' instead", test.expected, tokenString)
		}
	}
}
