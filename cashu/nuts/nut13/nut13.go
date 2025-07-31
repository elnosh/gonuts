package nut13

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

var (
	ErrCollidingKeysetId = errors.New("Error: colliding keyset Id")
)

func keysetIdToBigInt(keysetId string) (uint64, error) {
	keysetBytes, err := hex.DecodeString(keysetId)
	if err != nil {
		return 0, err
	}
	bigEndianBytes := binary.BigEndian.Uint64(keysetBytes)
	keysetIdInt := bigEndianBytes % (1<<31 - 1)
	return keysetIdInt, nil
}

func DeriveKeysetPath(master *hdkeychain.ExtendedKey, keysetId string) (*hdkeychain.ExtendedKey, error) {
	keysetIdInt, err := keysetIdToBigInt(keysetId)
	if err != nil {
		return nil, err
	}

	// m/129372
	purpose, err := master.Derive(hdkeychain.HardenedKeyStart + 129372)
	if err != nil {
		return nil, err
	}

	// m/129372'/0'
	coinType, err := purpose.Derive(hdkeychain.HardenedKeyStart + 0)
	if err != nil {
		return nil, err
	}

	// m/129372'/0'/keyset_k_int'
	keysetPath, err := coinType.Derive(hdkeychain.HardenedKeyStart + uint32(keysetIdInt))
	if err != nil {
		return nil, err
	}

	return keysetPath, nil
}

func DeriveBlindingFactor(keysetPath *hdkeychain.ExtendedKey, counter uint32) (*secp256k1.PrivateKey, error) {
	// m/129372'/0'/keyset_k_int'/counter'
	counterPath, err := keysetPath.Derive(hdkeychain.HardenedKeyStart + counter)
	if err != nil {
		return nil, err
	}

	// m/129372'/0'/keyset_k_int'/counter'/1
	rDerivationPath, err := counterPath.Derive(1)
	if err != nil {
		return nil, err
	}

	rkey, err := rDerivationPath.ECPrivKey()
	if err != nil {
		return nil, err
	}

	return rkey, nil
}

func DeriveSecret(keysetPath *hdkeychain.ExtendedKey, counter uint32) (string, error) {
	// m/129372'/0'/keyset_k_int'/counter'
	counterPath, err := keysetPath.Derive(hdkeychain.HardenedKeyStart + counter)
	if err != nil {
		return "", err
	}

	// m/129372'/0'/keyset_k_int'/counter'/0
	secretDerivationPath, err := counterPath.Derive(0)
	if err != nil {
		return "", err
	}

	secretKey, err := secretDerivationPath.ECPrivKey()
	if err != nil {
		return "", err
	}

	secretBytes := secretKey.Serialize()
	secret := hex.EncodeToString(secretBytes)

	return secret, nil
}

func CheckCollidingKeysets(currentKeysetIds []string, newMintKeysetIds []string) error {

	for i := range currentKeysetIds {
		keysetIdInt, err := keysetIdToBigInt(currentKeysetIds[i])
		if err != nil {
			return err
		}

		for j := range newMintKeysetIds {
			if currentKeysetIds[i] == newMintKeysetIds[j] {
				return fmt.Errorf("%w. KeysetId: %+v", ErrCollidingKeysetId, currentKeysetIds[i])
			}

			keysetIdIntToCompare, err := keysetIdToBigInt(newMintKeysetIds[j])
			if err != nil {
				return err
			}

			if keysetIdInt == keysetIdIntToCompare {
				return fmt.Errorf("%w. KeysetId: %+v", ErrCollidingKeysetId, currentKeysetIds[i])
			}
		}
	}

	return nil
}
