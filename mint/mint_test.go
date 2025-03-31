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

	if mint.activeKeyset.InputFeePpk != 100 {
		t.Fatalf("expected keyset with fee of %v but got %v", 100, mint.activeKeyset.InputFeePpk)
	}

	// rotate keyset
	config.RotateKeyset = true
	mint, _ = LoadMint(config)

	newActiveKeyset := mint.GetActiveKeyset()

	if len(mint.keysets) != 2 {
		t.Fatalf("expected keyset list length of 2 but got %v", len(mint.keysets))
	}

	if prevActive, ok := mint.keysets[firstActiveKeyset.Id]; !ok {
		t.Fatalf("previous existing keyset '%v' was not found", firstActiveKeyset.Id)
	} else {
		if prevActive.Active {
			t.Fatal("previous active keyset has active status")
		}
	}

	if mint.activeKeyset.Id != newActiveKeyset.Id {
		t.Fatalf("active keyset ids do not match. Expected '%v' but got '%v'",
			mint.activeKeyset.Id, newActiveKeyset.Id)
	}

	secondActiveKeyset := mint.GetActiveKeyset()

	// load without rotating keyset.
	config.RotateKeyset = false
	mint, _ = LoadMint(config)
	if len(mint.keysets) != 2 {
		t.Fatalf("expected keyset list length of 2 but got %v", len(mint.keysets))
	}

	if prevActive, ok := mint.keysets[firstActiveKeyset.Id]; !ok {
		t.Fatalf("previous existing keyset '%v' was not found", firstActiveKeyset.Id)
	} else {
		if prevActive.Active {
			t.Fatal("previous active keyset has active status")
		}
	}
	if mint.activeKeyset.Id != secondActiveKeyset.Id {
		t.Fatalf("active keyset ids do not match. Expected '%v' but got '%v'",
			mint.activeKeyset.Id, secondActiveKeyset.Id)
	}

	// rotate keyset again
	config.RotateKeyset = true
	config.InputFeePpk = 200
	mint, _ = LoadMint(config)

	newActiveKeyset = mint.GetActiveKeyset()

	if len(mint.keysets) != 3 {
		t.Fatalf("expected keyset list length of 3 but got %v", len(mint.keysets))
	}

	if _, ok := mint.keysets[firstActiveKeyset.Id]; !ok {
		t.Fatalf("previous existing keyset '%v' was not found", firstActiveKeyset.Id)
	}
	if _, ok := mint.keysets[secondActiveKeyset.Id]; !ok {
		t.Fatalf("previous existing keyset '%v' was not found", secondActiveKeyset.Id)
	}

	if mint.activeKeyset.Id != newActiveKeyset.Id {
		t.Fatalf("active keyset ids do not match. Expected '%v' but got '%v'",
			mint.activeKeyset.Id, newActiveKeyset.Id)
	}

	if mint.activeKeyset.InputFeePpk != 200 {
		t.Fatalf("expected fee of '%v' but got '%v'", 200, mint.activeKeyset.InputFeePpk)
	}
}
