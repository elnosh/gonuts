package wallet

import (
	"fmt"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// DeriveNostrKey derives a hardened key for Nostr communication using cointype 1237
// This should only be called when Nostr support is enabled
func DeriveNostrKey(masterKey *hdkeychain.ExtendedKey) (*secp256k1.PrivateKey, error) {
	if masterKey == nil {
		return nil, fmt.Errorf("master key cannot be nil")
	}

	// m/1237' - hardened cointype for Nostr
	coinType, err := masterKey.Derive(hdkeychain.HardenedKeyStart + 1237)
	if err != nil {
		return nil, fmt.Errorf("failed to derive cointype: %v", err)
	}

	// m/1237'/0' - hardened account
	account, err := coinType.Derive(hdkeychain.HardenedKeyStart + 0)
	if err != nil {
		return nil, fmt.Errorf("failed to derive account: %v", err)
	}

	// Get the btcec private key
	btcPrivKey, err := account.ECPrivKey()
	if err != nil {
		return nil, fmt.Errorf("failed to extract private key: %v", err)
	}

	// Convert to secp256k1 private key for Nostr
	secp256k1PrivKey := secp256k1.PrivKeyFromBytes(btcPrivKey.Serialize())

	return secp256k1PrivKey, nil
}
