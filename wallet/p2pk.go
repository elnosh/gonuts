package wallet

import (
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
)

// Derive key that wallet will use to receive locked ecash
func DeriveP2PK(key *hdkeychain.ExtendedKey) (*btcec.PrivateKey, error) {
	// m/129372'
	purpose, err := key.Derive(hdkeychain.HardenedKeyStart + 129372)
	if err != nil {
		return nil, err
	}

	// m/129372'/0'
	coinType, err := purpose.Derive(hdkeychain.HardenedKeyStart + 0)
	if err != nil {
		return nil, err
	}

	// m/129372'/0'/1'
	first, err := coinType.Derive(hdkeychain.HardenedKeyStart + 1)
	if err != nil {
		return nil, err
	}

	// m/129372'/0'/1'/0
	extKey, err := first.Derive(0)
	if err != nil {
		return nil, err
	}

	pk, err := extKey.ECPrivKey()
	if err != nil {
		return nil, err
	}

	return pk, nil
}
