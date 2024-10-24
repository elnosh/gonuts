package nut10

import (
	"reflect"
	"testing"
)

func TestSerializeSecret(t *testing.T) {
	tests := []struct {
		secret         WellKnownSecret
		expectedSecret string
	}{
		{
			secret: WellKnownSecret{
				Kind: P2PK,
				Data: SecretData{
					Nonce: "da62796403af76c80cd6ce9153ed3746",
					Data:  "033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e",
					Tags: [][]string{
						{"sigflag", "SIG_ALL"},
					},
				},
			},
			expectedSecret: `["P2PK", {"nonce":"da62796403af76c80cd6ce9153ed3746","data":"033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e","tags":[["sigflag","SIG_ALL"]]}]`,
		},
		{
			secret: WellKnownSecret{
				Kind: HTLC,
				Data: SecretData{
					Nonce: "da62796403af76c80cd6ce9153ed3746",
					Data:  "033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e",
					Tags: [][]string{
						{"pubkeys", "02698c4e2b5f9534cd0687d87513c759790cf829aa5739184a3e3735471fbda904"},
					},
				},
			},
			expectedSecret: `["HTLC", {"nonce":"da62796403af76c80cd6ce9153ed3746","data":"033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e","tags":[["pubkeys","02698c4e2b5f9534cd0687d87513c759790cf829aa5739184a3e3735471fbda904"]]}]`,
		},
	}

	for _, test := range tests {
		serialized, err := SerializeSecret(test.secret)
		if err != nil {
			t.Fatalf("got unexpected error: %v", err)
		}

		if serialized != test.expectedSecret {
			t.Fatalf("expected secret:\n%v\n\n but got:\n%v", test.expectedSecret, serialized)
		}
	}

}

func TestDeserializeSecret(t *testing.T) {
	tests := []struct {
		jsonSecret    string
		expectedKind  SecretKind
		expectedNonce string
		expectedData  string
		expectedTags  [][]string
	}{
		{
			jsonSecret:    `["P2PK", {"nonce":"da62796403af76c80cd6ce9153ed3746","data":"033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e","tags":[["sigflag","SIG_ALL"]]}]`,
			expectedKind:  P2PK,
			expectedNonce: "da62796403af76c80cd6ce9153ed3746",
			expectedData:  "033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e",
			expectedTags:  [][]string{{"sigflag", "SIG_ALL"}},
		},
		{
			jsonSecret:    `["HTLC", {"nonce":"da62796403af76c80cd6ce9153ed3746","data":"033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e","tags":[]}]`,
			expectedKind:  HTLC,
			expectedNonce: "da62796403af76c80cd6ce9153ed3746",
			expectedData:  "033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e",
			expectedTags:  [][]string{},
		},
	}

	for _, test := range tests {
		secret, err := DeserializeSecret(test.jsonSecret)
		if err != nil {
			t.Fatalf("got unexpected error: %v", err)
		}

		if secret.Kind != test.expectedKind {
			t.Fatalf("expected kind '%v' but got '%v' instead", test.expectedKind, secret.Kind)
		}
		if secret.Data.Nonce != test.expectedNonce {
			t.Fatalf("expected nonce '%v' but got '%v' instead", test.expectedNonce, secret.Data.Nonce)
		}
		if secret.Data.Data != test.expectedData {
			t.Fatalf("expected data '%v' but got '%v' instead", test.expectedData, secret.Data.Data)
		}
		if !reflect.DeepEqual(secret.Data.Tags, test.expectedTags) {
			t.Fatalf("expected tags '%v' but got '%v' instead", test.expectedTags, secret.Data.Tags)
		}
	}
}
