package mint

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut01"
	"github.com/elnosh/gonuts/cashu/nuts/nut02"
	"github.com/elnosh/gonuts/crypto"
	"github.com/gorilla/mux"
)

func TestActiveKeysetsHandler(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/v1/keys", nil)
	if err != nil {
		t.Fatalf("error creating request: %v", err)
	}

	seed, _ := hdkeychain.GenerateSeed(32)
	master, _ := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	activeKeyset, _ := crypto.GenerateKeyset(master, 0, 0, true)

	mint := &Mint{
		activeKeyset: activeKeyset,
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mintServer := MintServer{
		mint:  mint,
		cache: NewCache(),
	}

	w := httptest.NewRecorder()
	mintServer.getActiveKeysets(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status code %d but got %d", http.StatusOK, w.Code)
	}

	expectedKeysetResponse := nut01.GetKeysResponse{
		Keysets: []nut01.Keyset{
			{
				Id:   activeKeyset.Id,
				Unit: cashu.Sat.String(),
				Keys: activeKeyset.PublicKeys(),
			},
		},
	}

	expectedJson, _ := json.Marshal(expectedKeysetResponse)
	if !bytes.Equal(expectedJson, w.Body.Bytes()) {
		t.Fatal("responses do not match")
	}
}

func TestGetKeysetsHandler(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/v1/keysets", nil)
	if err != nil {
		t.Fatalf("error creating request: %v", err)
	}

	seed, _ := hdkeychain.GenerateSeed(32)
	master, _ := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	activeKeyset, _ := crypto.GenerateKeyset(master, 0, 150, true)
	inactiveKeyset, _ := crypto.GenerateKeyset(master, 1, 200, false)

	mint := &Mint{
		activeKeyset: activeKeyset,
		keysets: map[string]crypto.MintKeyset{
			activeKeyset.Id:   *activeKeyset,
			inactiveKeyset.Id: *inactiveKeyset,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mintServer := MintServer{mint: mint}

	w := httptest.NewRecorder()
	mintServer.getKeysetsList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status code %d but got %d", http.StatusOK, w.Code)
	}

	expectedKeysetsResponse := nut02.GetKeysetsResponse{
		Keysets: []nut02.Keyset{
			{
				Id:          activeKeyset.Id,
				Unit:        cashu.Sat.String(),
				Active:      true,
				InputFeePpk: 150,
			},
			{
				Id:          inactiveKeyset.Id,
				Unit:        cashu.Sat.String(),
				Active:      false,
				InputFeePpk: 200,
			},
		},
	}

	var keysetsResponse nut02.GetKeysetsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &keysetsResponse); err != nil {
		t.Fatal(err)
	}

	keysets := keysetsResponse.Keysets
	sort.Slice(keysets, func(i, j int) bool {
		return keysets[i].InputFeePpk < keysets[j].InputFeePpk
	})
	keysetsResponse.Keysets = keysets

	if !reflect.DeepEqual(expectedKeysetsResponse, keysetsResponse) {
		t.Fatalf("keyset responses do not match. Expected '%+v' but got '%+v'",
			expectedKeysetsResponse, keysetsResponse)
	}
}

func TestGetKeysetByIdHandler(t *testing.T) {
	seed, _ := hdkeychain.GenerateSeed(32)
	master, _ := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	activeKeyset, _ := crypto.GenerateKeyset(master, 0, 150, true)
	expectedActiveKeyset := nut01.GetKeysResponse{
		Keysets: []nut01.Keyset{
			{
				Id:   activeKeyset.Id,
				Unit: activeKeyset.Unit,
				Keys: activeKeyset.PublicKeys(),
			},
		},
	}
	expectedActiveJson, _ := json.Marshal(expectedActiveKeyset)

	inactiveKeyset, _ := crypto.GenerateKeyset(master, 1, 200, false)
	expectedInactiveKeyset := nut01.GetKeysResponse{
		Keysets: []nut01.Keyset{
			{
				Id:   inactiveKeyset.Id,
				Unit: inactiveKeyset.Unit,
				Keys: inactiveKeyset.PublicKeys(),
			},
		},
	}
	expectedInactiveJson, _ := json.Marshal(expectedInactiveKeyset)
	expectedKeysetNotFound, _ := json.Marshal(cashu.UnknownKeysetErr)

	mint := &Mint{
		activeKeyset: activeKeyset,
		keysets: map[string]crypto.MintKeyset{
			activeKeyset.Id:   *activeKeyset,
			inactiveKeyset.Id: *inactiveKeyset,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mintServer := MintServer{
		mint:  mint,
		cache: NewCache(),
	}
	r := mux.NewRouter()
	r.HandleFunc("/v1/keys/{id}", mintServer.getKeysetById)

	tests := []struct {
		name               string
		id                 string
		expectedStatusCode int
		expectedJson       []byte
	}{
		{
			name:               "active keyset",
			id:                 activeKeyset.Id,
			expectedStatusCode: http.StatusOK,
			expectedJson:       expectedActiveJson,
		},
		{
			name:               "inactive keyset",
			id:                 inactiveKeyset.Id,
			expectedStatusCode: http.StatusOK,
			expectedJson:       expectedInactiveJson,
		},
		{
			name:               "non existent keyset",
			id:                 "non-existent-id",
			expectedStatusCode: http.StatusBadRequest,
			expectedJson:       expectedKeysetNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "/v1/keys/"+test.id, nil)
			if err != nil {
				t.Fatalf("error creating request: %v", err)
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != test.expectedStatusCode {
				t.Errorf("expected status code %d but got %d", test.expectedStatusCode, w.Code)
			}

			if !bytes.Equal(test.expectedJson, w.Body.Bytes()) {
				t.Fatal("responses do not match")
			}
		})
	}
}
