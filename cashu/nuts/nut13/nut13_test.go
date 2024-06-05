package nut13

import (
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/tyler-smith/go-bip39"
)

func TestSecretDerivation(t *testing.T) {
	mnemonic := "half depart obvious quality work element tank gorilla view sugar picture humble"
	keysetId := "009a1f293253e41e"

	seed := bip39.NewSeed(mnemonic, "")
	master, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		t.Fatal(err)
	}

	keysetPath, err := DeriveKeysetPath(master, keysetId)
	if err != nil {
		t.Fatalf("could not derive keyset path: %v", err)
	}

	secrets := make([]string, 5)
	rs := make([]string, 5)

	var i uint32 = 0
	for ; i < 5; i++ {
		secret, err := DeriveSecret(keysetPath, i)
		if err != nil {
			t.Fatalf("error deriving secret: %v", err)
		}
		secrets[i] = secret

		rkey, err := DeriveBlindingFactor(keysetPath, i)
		if err != nil {
			t.Fatalf("error deriving r: %v", err)
		}

		rbytes := rkey.Serialize()
		r := hex.EncodeToString(rbytes)
		rs[i] = r
	}

	expectedSecrets := []string{
		"485875df74771877439ac06339e284c3acfcd9be7abf3bc20b516faeadfe77ae",
		"8f2b39e8e594a4056eb1e6dbb4b0c38ef13b1b2c751f64f810ec04ee35b77270",
		"bc628c79accd2364fd31511216a0fab62afd4a18ff77a20deded7b858c9860c8",
		"59284fd1650ea9fa17db2b3acf59ecd0f2d52ec3261dd4152785813ff27a33bf",
		"576c23393a8b31cc8da6688d9c9a96394ec74b40fdaf1f693a6bb84284334ea0",
	}

	expectedRs := []string{
		"ad00d431add9c673e843d4c2bf9a778a5f402b985b8da2d5550bf39cda41d679",
		"967d5232515e10b81ff226ecf5a9e2e2aff92d66ebc3edf0987eb56357fd6248",
		"b20f47bb6ae083659f3aa986bfa0435c55c6d93f687d51a01f26862d9b9a4899",
		"fb5fca398eb0b1deb955a2988b5ac77d32956155f1c002a373535211a2dfdc29",
		"5f09bfbfe27c439a597719321e061e2e40aad4a36768bb2bcc3de547c9644bf9",
	}

	for i := 0; i < 5; i++ {
		if expectedSecrets[i] != secrets[i] {
			t.Fatalf("secret at index: %v does not match. Expected '%v' but got '%v'", i, expectedSecrets[i], secrets[i])
		}

		if expectedRs[i] != rs[i] {
			t.Fatalf("r at index: %v does not match. Expected '%v' but got '%v'", i, expectedRs[i], rs[i])
		}
	}

}
