package wallet

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut07"
	"github.com/elnosh/gonuts/cashu/nuts/nut09"
	"github.com/elnosh/gonuts/cashu/nuts/nut13"
	"github.com/elnosh/gonuts/crypto"
	"github.com/elnosh/gonuts/wallet/client"
	"github.com/tyler-smith/go-bip39"
)

func Restore(walletPath, mnemonic string, mintsToRestore []string) (uint64, error) {
	// check if wallet db already exists, if there is one, throw error.
	dbpath := filepath.Join(walletPath, "wallet.db")
	_, err := os.Stat(dbpath)
	if err == nil {
		return 0, errors.New("wallet already exists")
	}

	if err := os.MkdirAll(walletPath, 0700); err != nil {
		return 0, err
	}

	// check mnemonic is valid
	if !bip39.IsMnemonicValid(mnemonic) {
		return 0, errors.New("invalid mnemonic")
	}

	// create wallet db
	db, err := InitStorage(walletPath)
	if err != nil {
		return 0, fmt.Errorf("error restoring wallet: %v", err)
	}

	seed := bip39.NewSeed(mnemonic, "")
	// get master key from seed
	masterKey, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return 0, err
	}
	db.SaveMnemonicSeed(mnemonic, seed)

	proofsRestored := cashu.Proofs{}

	// for each mint get the keysets and do restore process for each keyset
	for _, mint := range mintsToRestore {
		mintInfo, err := client.GetMintInfo(mint)
		if err != nil {
			return 0, fmt.Errorf("error getting info from mint: %v", err)
		}

		nut7, ok := mintInfo.Nuts[7].(map[string]interface{})
		nut9, ok2 := mintInfo.Nuts[9].(map[string]interface{})
		if !ok || !ok2 || nut7["supported"] != true || nut9["supported"] != true {
			fmt.Println("mint does not support the necessary operations to restore wallet")
			continue
		}

		// call to get mint keysets
		keysetsResponse, err := client.GetAllKeysets(mint)
		if err != nil {
			return 0, err
		}

		for _, keyset := range keysetsResponse.Keysets {
			if keyset.Unit != cashu.Sat.String() {
				break
			}

			_, err := hex.DecodeString(keyset.Id)
			// ignore keysets with non-hex ids
			if err != nil {
				continue
			}

			var counter uint32 = 0

			keysetKeys, err := GetKeysetKeys(mint, keyset.Id)
			if err != nil {
				return 0, err
			}

			walletKeyset := crypto.WalletKeyset{
				Id:         keyset.Id,
				MintURL:    mint,
				Unit:       keyset.Unit,
				Active:     keyset.Active,
				PublicKeys: keysetKeys,
				Counter:    counter,
			}

			if err := db.SaveKeyset(&walletKeyset); err != nil {
				return 0, err
			}

			keysetDerivationPath, err := nut13.DeriveKeysetPath(masterKey, keyset.Id)
			if err != nil {
				return 0, err
			}

			// stop when it reaches 3 consecutive empty batches
			emptyBatches := 0
			for emptyBatches < 3 {
				blindedMessages := make(cashu.BlindedMessages, 100)
				rs := make([]*secp256k1.PrivateKey, 100)
				secrets := make([]string, 100)

				// create batch of 100 blinded messages
				for i := 0; i < 100; i++ {
					secret, r, err := generateDeterministicSecret(keysetDerivationPath, counter)
					if err != nil {
						return 0, err
					}
					B_, r, err := crypto.BlindMessage(secret, r)
					if err != nil {
						return 0, err
					}

					B_str := hex.EncodeToString(B_.SerializeCompressed())
					blindedMessages[i] = cashu.BlindedMessage{B_: B_str, Id: keyset.Id}
					rs[i] = r
					secrets[i] = secret
					counter++
				}

				// if response has signatures, unblind them and check proof states
				restoreRequest := nut09.PostRestoreRequest{Outputs: blindedMessages}
				restoreResponse, err := client.PostRestore(mint, restoreRequest)
				if err != nil {
					return 0, fmt.Errorf("error restoring signatures from mint '%v': %v", mint, err)
				}

				if len(restoreResponse.Signatures) == 0 {
					emptyBatches++
					break
				}

				Ys := make([]string, len(restoreResponse.Signatures))
				proofs := make(map[string]cashu.Proof, len(restoreResponse.Signatures))

				// unblind signatures
				for i, signature := range restoreResponse.Signatures {
					pubkey, ok := keysetKeys[signature.Amount]
					if !ok {
						return 0, errors.New("key not found")
					}

					C, err := unblindSignature(signature.C_, rs[i], pubkey)
					if err != nil {
						return 0, err
					}

					Y, err := crypto.HashToCurve([]byte(secrets[i]))
					if err != nil {
						return 0, err
					}
					Yhex := hex.EncodeToString(Y.SerializeCompressed())
					Ys[i] = Yhex

					proof := cashu.Proof{
						Amount: signature.Amount,
						Secret: secrets[i],
						C:      C,
						Id:     signature.Id,
					}
					proofs[Yhex] = proof
				}

				proofStateRequest := nut07.PostCheckStateRequest{Ys: Ys}
				proofStateResponse, err := client.PostCheckProofState(mint, proofStateRequest)
				if err != nil {
					return 0, err
				}

				for _, proofState := range proofStateResponse.States {
					// NUT-07 can also respond with witness data. Since not supporting this yet, ignore proofs that have witness
					if len(proofState.Witness) > 0 {
						break
					}

					// save unspent proofs
					if proofState.State == nut07.Unspent {
						proof := proofs[proofState.Y]
						proofsRestored = append(proofsRestored, proof)
					}
				}
				if err := db.SaveProofs(proofsRestored); err != nil {
					return 0, fmt.Errorf("error saving restored proofs: %v", err)
				}

				// save wallet keyset with latest counter moving forward for wallet
				if err := db.IncrementKeysetCounter(keyset.Id, counter); err != nil {
					return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
				}
				emptyBatches = 0
			}
		}
	}

	return proofsRestored.Amount(), nil
}
