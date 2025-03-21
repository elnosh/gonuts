package mint

import (
	"os"
	"testing"

	"github.com/elnosh/gonuts/mint/lightning"
)

func TestKeysetRotations(t *testing.T) {
	fakeBackend := lightning.FakeBackend{}
	testMintPath := "./testmintkeysetrotations"
	config := Config{
		RotateKeyset:    true,
		MintPath:        testMintPath,
		InputFeePpk:     100,
		LightningClient: &fakeBackend,
		LogLevel:        Disable,
	}
	defer os.RemoveAll(testMintPath)

	mint, _ := LoadMint(config)
	firstActiveKeyset := mint.GetActiveKeyset()

	if len(mint.activeKeysets) != 1 || len(mint.keysets) != 1 {
		t.Fatal("length of keysets list is not 1")
	}
	if k, _ := mint.activeKeysets[firstActiveKeyset.Id]; k.InputFeePpk != 100 {
		t.Fatalf("expected keyset with fee of %v but got %v", 100, k.InputFeePpk)
	}

	// rotate keyset
	config.RotateKeyset = true
	mint, _ = LoadMint(config)

	newActiveKeyset := mint.GetActiveKeyset()

	if len(mint.activeKeysets) != 1 {
		t.Fatal("length of active keysets list is not 1")
	}
	if len(mint.keysets) != 2 {
		t.Fatalf("expected keyset list length of 2 but got %v", len(mint.keysets))
	}

	if _, ok := mint.keysets[firstActiveKeyset.Id]; !ok {
		t.Fatalf("previous existing keyset '%v' was not found", firstActiveKeyset.Id)
	}
	if _, ok := mint.activeKeysets[firstActiveKeyset.Id]; ok {
		t.Fatalf("deactived keyset '%v' was found in list of active ones", firstActiveKeyset.Id)
	}
	if _, ok := mint.activeKeysets[newActiveKeyset.Id]; !ok {
		t.Fatalf("expected active keyset '%v' was found in list of active ones", newActiveKeyset.Id)
	}

	secondActiveKeyset := mint.GetActiveKeyset()

	// load without rotating keyset.
	config.RotateKeyset = false
	mint, _ = LoadMint(config)
	if len(mint.activeKeysets) != 1 {
		t.Fatal("length of active keysets list is not 1")
	}
	if len(mint.keysets) != 2 {
		t.Fatalf("expected keyset list length of 2 but got %v", len(mint.keysets))
	}

	if _, ok := mint.keysets[firstActiveKeyset.Id]; !ok {
		t.Fatalf("previous existing keyset '%v' was not found", firstActiveKeyset.Id)
	}
	if _, ok := mint.activeKeysets[firstActiveKeyset.Id]; ok {
		t.Fatalf("deactived keyset '%v' was found in list of active ones", firstActiveKeyset.Id)
	}

	// rotate keyset again
	config.RotateKeyset = true
	config.InputFeePpk = 200
	mint, _ = LoadMint(config)

	activeKeyset := mint.GetActiveKeyset()

	if len(mint.activeKeysets) != 1 {
		t.Fatal("length of active keysets list is not 1")
	}
	if len(mint.keysets) != 3 {
		t.Fatalf("expected keyset list length of 3 but got %v", len(mint.keysets))
	}

	if _, ok := mint.keysets[firstActiveKeyset.Id]; !ok {
		t.Fatalf("previous existing keyset '%v' was not found", firstActiveKeyset.Id)
	}
	if _, ok := mint.keysets[secondActiveKeyset.Id]; !ok {
		t.Fatalf("previous existing keyset '%v' was not found", secondActiveKeyset.Id)
	}
	if _, ok := mint.activeKeysets[secondActiveKeyset.Id]; ok {
		t.Fatalf("deactived keyset '%v' was found in list of active ones", secondActiveKeyset.Id)
	}

	newActive, ok := mint.activeKeysets[activeKeyset.Id]
	if !ok {
		t.Fatalf("expected active keyset '%v' is not in list of active ones", activeKeyset.Id)
	}
	if newActive.InputFeePpk != 200 {
		t.Fatalf("expected fee of '%v' but got '%v'", 200, newActive.InputFeePpk)
	}
}
