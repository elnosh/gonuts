package wallet

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/crypto"
)

// GetMintActiveKeyset gets the active keyset with the specified unit
func GetMintActiveKeyset(mintURL string, unit cashu.Unit) (*crypto.WalletKeyset, error) {
	keysets, err := GetAllKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active keysets from mint: %v", err)
	}

	keysetsResponse, err := GetActiveKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting active keysets from mint: %v", err)
	}

	for i, keyset := range keysetsResponse.Keysets {
		if keyset.Unit == unit.String() {
			var inputFeePpk uint
			for _, response := range keysets.Keysets {
				if response.Id == keyset.Id {
					inputFeePpk = response.InputFeePpk
					break
				}
			}

			_, err := hex.DecodeString(keyset.Id)
			if keyset.Unit == cashu.Sat.String() && err == nil {
				keys, err := crypto.MapPubKeys(keysetsResponse.Keysets[i].Keys)
				if err != nil {
					return nil, err
				}
				id := crypto.DeriveKeysetId(keys)
				if id != keyset.Id {
					return nil, fmt.Errorf("Got invalid keyset. Derived id: '%v' but got '%v' from mint", id, keyset.Id)
				}

				return &crypto.WalletKeyset{
					Id:          id,
					MintURL:     mintURL,
					Unit:        keyset.Unit,
					Active:      true,
					PublicKeys:  keys,
					InputFeePpk: inputFeePpk,
				}, nil
			}
		}
	}

	return nil, errors.New("could not find an active keyset for the unit")
}

func GetMintInactiveKeysets(mintURL string) (map[string]crypto.WalletKeyset, error) {
	keysetsResponse, err := GetAllKeysets(mintURL)
	if err != nil {
		return nil, fmt.Errorf("error getting keysets from mint: %v", err)
	}

	inactiveKeysets := make(map[string]crypto.WalletKeyset)
	for _, keysetRes := range keysetsResponse.Keysets {
		_, err := hex.DecodeString(keysetRes.Id)
		if !keysetRes.Active && keysetRes.Unit == cashu.Sat.String() && err == nil {
			keyset := crypto.WalletKeyset{
				Id:          keysetRes.Id,
				MintURL:     mintURL,
				Unit:        keysetRes.Unit,
				Active:      keysetRes.Active,
				InputFeePpk: keysetRes.InputFeePpk,
			}
			inactiveKeysets[keyset.Id] = keyset
		}
	}
	return inactiveKeysets, nil
}

// getActiveSatKeyset returns the active sat keyset for the mint passed.
// if mint passed is known and the latest active sat keyset has changed,
// it will inactivate the previous active and save new active to db
func (w *Wallet) getActiveSatKeyset(mintURL string) (*crypto.WalletKeyset, error) {
	mint, ok := w.mints[mintURL]
	// if mint is not known, get active sat keyset from calling mint
	if !ok {
		activeKeyset, err := GetMintActiveKeyset(mintURL, w.unit)
		if err != nil {
			return nil, err
		}
		return activeKeyset, nil
	}

	allKeysets, err := GetAllKeysets(mintURL)
	if err != nil {
		return nil, err
	}

	activeKeyset := mint.activeKeyset
	// check if there is new active keyset
	activeChanged := true
	for _, keyset := range allKeysets.Keysets {
		if keyset.Active && keyset.Id == activeKeyset.Id {
			activeChanged = false
			break
		}
	}

	// if new active, save it to db and inactivate previous
	if activeChanged {
		// inactivate previous active
		activeKeyset.Active = false
		w.mints[mintURL].inactiveKeysets[activeKeyset.Id] = activeKeyset
		if err := w.db.SaveKeyset(&activeKeyset); err != nil {
			return nil, err
		}

		for _, keyset := range allKeysets.Keysets {
			_, err = hex.DecodeString(keyset.Id)
			if keyset.Active && keyset.Unit == w.unit.String() && err == nil {
				keysetKeys, err := GetKeysetById(mintURL, keyset.Id)
				if err != nil {
					return nil, err
				}

				keys, err := crypto.MapPubKeys(keysetKeys.Keysets[0].Keys)
				if err != nil {
					return nil, err
				}

				activeKeyset = crypto.WalletKeyset{
					Id:          keyset.Id,
					MintURL:     mintURL,
					Unit:        keyset.Unit,
					Active:      true,
					PublicKeys:  keys,
					InputFeePpk: keyset.InputFeePpk,
				}

				if err := w.db.SaveKeyset(&activeKeyset); err != nil {
					return nil, err
				}
				mint.activeKeyset = activeKeyset
				w.mints[mintURL] = mint
			}
		}
	}

	return &activeKeyset, nil
}

func getKeysetKeys(mintURL, id string) (map[uint64]*secp256k1.PublicKey, error) {
	keysetsResponse, err := GetKeysetById(mintURL, id)
	if err != nil {
		return nil, fmt.Errorf("error getting keyset from mint: %v", err)
	}

	var keys map[uint64]*secp256k1.PublicKey
	if len(keysetsResponse.Keysets) > 0 && keysetsResponse.Keysets[0].Unit == cashu.Sat.String() {
		var err error
		keys, err = crypto.MapPubKeys(keysetsResponse.Keysets[0].Keys)
		if err != nil {
			return nil, err
		}
	}

	return keys, nil
}
