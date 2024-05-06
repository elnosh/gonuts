package wallet

import (
	"testing"

	"github.com/elnosh/gonuts/crypto"
)

func TestCreateBlindedMessages(t *testing.T) {
	keyset := crypto.Keyset{Id: "009a1f293253e41e"}

	tests := []struct {
		amount uint64
		keyset crypto.Keyset
	}{
		{420, keyset},
		{10000000, keyset},
		{2500, keyset},
	}

	for _, test := range tests {
		blindedMessages, _, _, _ := createBlindedMessages(test.amount, test.keyset)
		amount := blindedMessages.Amount()
		if amount != test.amount {
			t.Errorf("expected '%v' but got '%v' instead", test.amount, amount)
		}

		for _, message := range blindedMessages {
			if message.Id != test.keyset.Id {
				t.Errorf("expected '%v' but got '%v' instead", test.keyset.Id, message.Id)
			}
		}
	}
}
