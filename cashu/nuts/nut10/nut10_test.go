package nut10

import (
	"reflect"
	"testing"

	"github.com/elnosh/gonuts/cashu"
)

func TestSerializeSecret(t *testing.T) {
	secretData := WellKnownSecret{
		Nonce: "da62796403af76c80cd6ce9153ed3746",
		Data:  "033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e",
		Tags: [][]string{
			{"sigflag", "SIG_ALL"},
		},
	}

	serialized, err := SerializeSecret(cashu.P2PK, secretData)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}

	expected := `["P2PK", {"nonce":"da62796403af76c80cd6ce9153ed3746","data":"033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e","tags":[["sigflag","SIG_ALL"]]}]`

	if serialized != expected {
		t.Fatalf("expected secret:\n%v\n\n but got:\n%v", expected, serialized)
	}
}

func TestDeserializeSecret(t *testing.T) {
	secret := `["P2PK", {"nonce":"da62796403af76c80cd6ce9153ed3746","data":"033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e","tags":[["sigflag","SIG_ALL"]]}]`
	secretData, err := DeserializeSecret(secret)
	if err != nil {
		t.Fatalf("got unexpected error: %v", err)
	}

	expectedNonce := "da62796403af76c80cd6ce9153ed3746"
	if secretData.Nonce != expectedNonce {
		t.Fatalf("expected nonce '%v' but got '%v' instead", expectedNonce, secretData.Nonce)
	}

	expectedData := "033281c37677ea273eb7183b783067f5244933ef78d8c3f15b1a77cb246099c26e"
	if secretData.Data != expectedData {
		t.Fatalf("expected data '%v' but got '%v' instead", expectedData, secretData.Data)
	}

	expectedTags := [][]string{
		{"sigflag", "SIG_ALL"},
	}
	if !reflect.DeepEqual(secretData.Tags, expectedTags) {
		t.Fatalf("expected tags '%v' but got '%v' instead", expectedTags, secretData.Tags)
	}
}
