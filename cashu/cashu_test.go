package cashu

import (
	"testing"
)

func TestDecodeToken(t *testing.T) {
	tests := []struct {
		tokenString string
		expected    Token
	}{
		{
			tokenString: "cashuAeyJ0b2tlbiI6W3sibWludCI6Imh0dHBzOi8vODMzMy5zcGFjZTozMzM4IiwicHJvb2ZzIjpbeyJhbW91bnQiOjIsImlkIjoiMDA5YTFmMjkzMjUzZTQxZSIsInNlY3JldCI6IjQwNzkxNWJjMjEyYmU2MWE3N2UzZTZkMmFlYjRjNzI3OTgwYmRhNTFjZDA2YTZhZmMyOWUyODYxNzY4YTc4MzciLCJDIjoiMDJiYzkwOTc5OTdkODFhZmIyY2M3MzQ2YjVlNDM0NWE5MzQ2YmQyYTUwNmViNzk1ODU5OGE3MmYwY2Y4NTE2M2VhIn0seyJhbW91bnQiOjgsImlkIjoiMDA5YTFmMjkzMjUzZTQxZSIsInNlY3JldCI6ImZlMTUxMDkzMTRlNjFkNzc1NmIwZjhlZTBmMjNhNjI0YWNhYTNmNGUwNDJmNjE0MzNjNzI4YzcwNTdiOTMxYmUiLCJDIjoiMDI5ZThlNTA1MGI4OTBhN2Q2YzA5NjhkYjE2YmMxZDVkNWZhMDQwZWExZGUyODRmNmVjNjlkNjEyOTlmNjcxMDU5In1dfV0sInVuaXQiOiJzYXQiLCJtZW1vIjoiVGhhbmsgeW91LiJ9",
			expected: Token{
				Token: []TokenProof{
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
		},
	}

	for _, test := range tests {
		token, _ := DecodeToken(test.tokenString)
		if token.Unit != test.expected.Unit {
			t.Errorf("expected '%v' but got '%v' instead", test.expected.Unit, token.Unit)
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

func TestTokenToString(t *testing.T) {
	tests := []struct {
		token    Token
		expected string
	}{
		{
			token: Token{
				Token: []TokenProof{
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
		tokenString := test.token.ToString()

		if tokenString != test.expected {
			t.Errorf("expected '%v'\n\n but got '%v' instead", test.expected, tokenString)
		}
	}
}

func TestSecretType(t *testing.T) {
	tests := []struct {
		proof          Proof
		expectedKind   SecretKind
		expectedIsP2PK bool
	}{
		{
			proof:          Proof{Secret: `["P2PK", {"nonce":"da62796403af76c80cd6ce9153ed3746","data":"033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e","tags":[["sigflag","SIG_ALL"]]}]`},
			expectedKind:   P2PK,
			expectedIsP2PK: true,
		},

		{
			proof:          Proof{Secret: `["DIFFERENT", {"nonce":"da62796403af76c80cd6ce9153ed3746","data":"033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e","tags":[]}]`},
			expectedKind:   Random,
			expectedIsP2PK: false,
		},

		{
			proof:          Proof{Secret: `someranadomsecret`},
			expectedKind:   Random,
			expectedIsP2PK: false,
		},
	}

	for _, test := range tests {
		kind := test.proof.SecretType()
		if kind != test.expectedKind {
			t.Fatalf("expected '%v' but got '%v' instead", test.expectedKind.String(), kind.String())
		}

		isP2PK := test.proof.IsSecretP2PK()
		if isP2PK != test.expectedIsP2PK {
			t.Fatalf("expected '%v' but got '%v' instead", test.expectedIsP2PK, isP2PK)
		}
	}
}
