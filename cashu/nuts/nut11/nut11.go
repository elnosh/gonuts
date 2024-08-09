package nut11

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
)

const (
	// supported tags
	SIGFLAG  = "sigflag"
	NSIGS    = "n_sigs"
	PUBKEYS  = "pubkeys"
	LOCKTIME = "locktime"
	REFUND   = "refund"

	// SIGFLAG types
	SIGINPUTS = "SIG_INPUTS"
	SIGALL    = "SIG_ALL"

	// Error code
	NUT11ErrCode cashu.CashuErrCode = 30001
)

type SigFlag int

const (
	SigInputs SigFlag = iota
	SigAll
	Unknown
)

// errors
var (
	InvalidTagErr            = cashu.Error{Detail: "invalid tag", Code: NUT11ErrCode}
	TooManyTagsErr           = cashu.Error{Detail: "too many tags", Code: NUT11ErrCode}
	NSigsMustBePositiveErr   = cashu.Error{Detail: "n_sigs must be a positive integer", Code: NUT11ErrCode}
	EmptyPubkeysErr          = cashu.Error{Detail: "pubkeys tag cannot be empty if n_sigs tag is present", Code: NUT11ErrCode}
	EmptyWitnessErr          = cashu.Error{Detail: "witness cannot be empty", Code: NUT11ErrCode}
	NotEnoughSignaturesErr   = cashu.Error{Detail: "not enough valid signatures provided", Code: NUT11ErrCode}
	AllSigAllFlagsErr        = cashu.Error{Detail: "all flags must be SIG_ALL", Code: NUT11ErrCode}
	SigAllKeysMustBeEqualErr = cashu.Error{Detail: "all public keys must be the same for SIG_ALL", Code: NUT11ErrCode}
	SigAllOnlySwap           = cashu.Error{Detail: "SIG_ALL can only be used in /swap operation", Code: NUT11ErrCode}
	NSigsMustBeEqualErr      = cashu.Error{Detail: "all n_sigs must be the same for SIG_ALL", Code: NUT11ErrCode}
)

type P2PKWitness struct {
	Signatures []string `json:"signatures"`
}

type P2PKTags struct {
	Sigflag  string
	NSigs    int
	Pubkeys  []*btcec.PublicKey
	Locktime int64
	Refund   []*btcec.PublicKey
}

// P2PKSecret returns a secret with a spending condition
// that will lock ecash to a public key
func P2PKSecret(pubkey string) (string, error) {
	// generate random nonce
	nonceBytes := make([]byte, 32)
	_, err := rand.Read(nonceBytes)
	if err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(nonceBytes)

	secretData := nut10.WellKnownSecret{
		Nonce: nonce,
		Data:  pubkey,
	}

	secret, err := nut10.SerializeSecret(nut10.P2PK, secretData)
	if err != nil {
		return "", err
	}

	return secret, nil
}

func ParseP2PKTags(tags [][]string) (*P2PKTags, error) {
	if len(tags) > 5 {
		return nil, TooManyTagsErr
	}

	p2pkTags := P2PKTags{}

	for _, tag := range tags {
		if len(tag) < 2 {
			return nil, InvalidTagErr
		}
		tagType := tag[0]
		switch tagType {
		case SIGFLAG:
			sigflagType := tag[1]
			if sigflagType == SIGINPUTS || sigflagType == SIGALL {
				p2pkTags.Sigflag = sigflagType
			} else {
				errmsg := fmt.Sprintf("invalig sigflag: %v", sigflagType)
				return nil, cashu.BuildCashuError(errmsg, NUT11ErrCode)
			}
		case NSIGS:
			nstr := tag[1]
			nsig, err := strconv.ParseInt(nstr, 10, 8)
			if err != nil {
				errmsg := fmt.Sprintf("invalig n_sigs value: %v", err)
				return nil, cashu.BuildCashuError(errmsg, NUT11ErrCode)
			}
			if nsig < 0 {
				return nil, NSigsMustBePositiveErr
			}
			p2pkTags.NSigs = int(nsig)
		case PUBKEYS:
			pubkeys := make([]*btcec.PublicKey, len(tag)-1)
			j := 0
			for i := 1; i < len(tag); i++ {
				pubkey, err := ParsePublicKey(tag[i])
				if err != nil {
					return nil, err
				}
				pubkeys[j] = pubkey
				j++
			}
			p2pkTags.Pubkeys = pubkeys
		case LOCKTIME:
			locktimestr := tag[1]
			locktime, err := strconv.ParseInt(locktimestr, 10, 64)
			if err != nil {
				errmsg := fmt.Sprintf("invalid locktime: %v", err)
				return nil, cashu.BuildCashuError(errmsg, NUT11ErrCode)
			}
			p2pkTags.Locktime = locktime
		case REFUND:
			refundKeys := make([]*btcec.PublicKey, len(tag)-1)
			j := 0
			for i := 1; i < len(tag); i++ {
				pubkey, err := ParsePublicKey(tag[i])
				if err != nil {
					return nil, err
				}
				refundKeys[j] = pubkey
				j++
			}
			p2pkTags.Refund = refundKeys
		}
	}

	return &p2pkTags, nil
}

func AddSignatureToInputs(inputs cashu.Proofs, signingKey *btcec.PrivateKey) (cashu.Proofs, error) {
	for i, proof := range inputs {
		hash := sha256.Sum256([]byte(proof.Secret))
		signature, err := schnorr.Sign(signingKey, hash[:])
		if err != nil {
			return nil, err
		}
		signatureBytes := signature.Serialize()

		p2pkWitness := P2PKWitness{
			Signatures: []string{hex.EncodeToString(signatureBytes)},
		}

		witness, err := json.Marshal(p2pkWitness)
		if err != nil {
			return nil, err
		}
		proof.Witness = string(witness)
		inputs[i] = proof
	}

	return inputs, nil
}

func AddSignatureToOutputs(
	outputs cashu.BlindedMessages,
	signingKey *btcec.PrivateKey,
) (cashu.BlindedMessages, error) {
	for i, output := range outputs {
		msgToSign, err := hex.DecodeString(output.B_)
		if err != nil {
			return nil, err
		}

		hash := sha256.Sum256(msgToSign)
		signature, err := schnorr.Sign(signingKey, hash[:])
		if err != nil {
			return nil, err
		}
		signatureBytes := signature.Serialize()

		p2pkWitness := P2PKWitness{
			Signatures: []string{hex.EncodeToString(signatureBytes)},
		}

		witness, err := json.Marshal(p2pkWitness)
		if err != nil {
			return nil, err
		}
		output.Witness = string(witness)
		outputs[i] = output
	}

	return outputs, nil
}

// PublicKeys returns a list of public keys that can sign
// a P2PK locked proof
func PublicKeys(secret nut10.WellKnownSecret) ([]*btcec.PublicKey, error) {
	p2pkTags, err := ParseP2PKTags(secret.Tags)
	if err != nil {
		return nil, err
	}

	pubkey, err := ParsePublicKey(secret.Data)
	if err != nil {
		return nil, err
	}
	pubkeys := append([]*btcec.PublicKey{pubkey}, p2pkTags.Pubkeys...)
	return pubkeys, nil
}

func IsSecretP2PK(proof cashu.Proof) bool {
	return nut10.SecretType(proof) == nut10.P2PK
}

// ProofsSigAll returns true if at least one of the proofs
// in the list has a SIG_ALL flag
func ProofsSigAll(proofs cashu.Proofs) bool {
	for _, proof := range proofs {
		secret, err := nut10.DeserializeSecret(proof.Secret)
		if err != nil {
			return false
		}

		if IsSigAll(secret) {
			return true
		}
	}
	return false
}

func IsSigAll(secret nut10.WellKnownSecret) bool {
	for _, tag := range secret.Tags {
		if len(tag) == 2 {
			if tag[0] == SIGFLAG && tag[1] == SIGALL {
				return true
			}
		}
	}

	return false
}

func CanSign(secret nut10.WellKnownSecret, key *btcec.PrivateKey) bool {
	publicKey, err := ParsePublicKey(secret.Data)
	if err != nil {
		return false
	}

	if reflect.DeepEqual(publicKey.SerializeCompressed(), key.PubKey().SerializeCompressed()) {
		return true
	}

	return false
}

func HasValidSignatures(hash []byte, witness P2PKWitness, Nsigs int, pubkeys []*btcec.PublicKey) bool {
	pubkeysCopy := make([]*btcec.PublicKey, len(pubkeys))
	copy(pubkeysCopy, pubkeys)

	validSignatures := 0
	for _, signature := range witness.Signatures {
		sig, err := ParseSignature(signature)
		if err != nil {
			continue
		}

		for i, pubkey := range pubkeysCopy {
			if sig.Verify(hash, pubkey) {
				validSignatures++
				if len(pubkeysCopy) > 1 {
					pubkeysCopy = slices.Delete(pubkeysCopy, i, i+1)
				}
				break
			}
		}
	}

	return validSignatures >= Nsigs
}

func ParsePublicKey(key string) (*btcec.PublicKey, error) {
	hexPubkey, err := hex.DecodeString(key)
	if err != nil {
		errmsg := fmt.Sprintf("invalid public key: %v", err)
		return nil, cashu.BuildCashuError(errmsg, NUT11ErrCode)
	}
	pubkey, err := btcec.ParsePubKey(hexPubkey)
	if err != nil {
		errmsg := fmt.Sprintf("invalid public key: %v", err)
		return nil, cashu.BuildCashuError(errmsg, NUT11ErrCode)
	}
	return pubkey, nil
}

func ParseSignature(signature string) (*schnorr.Signature, error) {
	hexSig, err := hex.DecodeString(signature)
	if err != nil {
		errmsg := fmt.Sprintf("invalid signature: %v", err)
		return nil, cashu.BuildCashuError(errmsg, NUT11ErrCode)
	}
	sig, err := schnorr.ParseSignature(hexSig)
	if err != nil {
		errmsg := fmt.Sprintf("invalid signature: %v", err)
		return nil, cashu.BuildCashuError(errmsg, NUT11ErrCode)
	}

	return sig, nil
}
