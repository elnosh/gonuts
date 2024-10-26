package nut11

import (
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

// NUT-11 specific errors
var (
	InvalidTagErr            = cashu.Error{Detail: "invalid tag", Code: NUT11ErrCode}
	TooManyTagsErr           = cashu.Error{Detail: "too many tags", Code: NUT11ErrCode}
	NSigsMustBePositiveErr   = cashu.Error{Detail: "n_sigs must be a positive integer", Code: NUT11ErrCode}
	EmptyPubkeysErr          = cashu.Error{Detail: "pubkeys tag cannot be empty if n_sigs tag is present", Code: NUT11ErrCode}
	InvalidWitness           = cashu.Error{Detail: "invalid witness", Code: NUT11ErrCode}
	InvalidKindErr           = cashu.Error{Detail: "invalid kind in secret", Code: NUT11ErrCode}
	DuplicateSignaturesErr   = cashu.Error{Detail: "witness has duplicate signatures", Code: NUT11ErrCode}
	NotEnoughSignaturesErr   = cashu.Error{Detail: "not enough valid signatures provided", Code: NUT11ErrCode}
	NoSignaturesErr          = cashu.Error{Detail: "no signatures provided in witness", Code: NUT11ErrCode}
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

func SerializeP2PKTags(p2pkTags P2PKTags) [][]string {
	var tags [][]string
	if len(p2pkTags.Sigflag) > 0 {
		tags = append(tags, []string{SIGFLAG, p2pkTags.Sigflag})
	}
	if p2pkTags.NSigs > 0 {
		numStr := strconv.Itoa(p2pkTags.NSigs)
		tags = append(tags, []string{NSIGS, numStr})
	}
	if len(p2pkTags.Pubkeys) > 0 {
		pubkeys := []string{PUBKEYS}
		for _, pubkey := range p2pkTags.Pubkeys {
			key := hex.EncodeToString(pubkey.SerializeCompressed())
			pubkeys = append(pubkeys, key)
		}
		tags = append(tags, pubkeys)
	}
	if p2pkTags.Locktime > 0 {
		locktime := strconv.FormatInt(p2pkTags.Locktime, 10)
		tags = append(tags, []string{LOCKTIME, locktime})
	}
	if len(p2pkTags.Refund) > 0 {
		refundKeys := []string{REFUND}
		for _, pubkey := range p2pkTags.Refund {
			key := hex.EncodeToString(pubkey.SerializeCompressed())
			refundKeys = append(refundKeys, key)
		}
		tags = append(tags, refundKeys)
	}
	return tags
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
// a P2PK or HTLC proof
func PublicKeys(secret nut10.WellKnownSecret) ([]*btcec.PublicKey, error) {
	p2pkTags, err := ParseP2PKTags(secret.Data.Tags)
	if err != nil {
		return nil, err
	}

	pubkeys := p2pkTags.Pubkeys
	// if P2PK proof, add key from data field
	if secret.Kind == nut10.P2PK {
		pubkey, err := ParsePublicKey(secret.Data.Data)
		if err != nil {
			return nil, err
		}
		pubkeys = append(pubkeys, pubkey)
	}
	return pubkeys, nil
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
	for _, tag := range secret.Data.Tags {
		if len(tag) == 2 {
			if tag[0] == SIGFLAG && tag[1] == SIGALL {
				return true
			}
		}
	}

	return false
}

func CanSign(secret nut10.WellKnownSecret, key *btcec.PrivateKey) bool {
	publicKey, err := ParsePublicKey(secret.Data.Data)
	if err != nil {
		return false
	}

	if reflect.DeepEqual(publicKey.SerializeCompressed(), key.PubKey().SerializeCompressed()) {
		return true
	}

	return false
}

func DuplicateSignatures(signatures []string) bool {
	sigs := make(map[string]bool)
	for _, sig := range signatures {
		if sigs[sig] {
			return true
		} else {
			sigs[sig] = true
		}
	}
	return false
}

func HasValidSignatures(hash []byte, signatures []string, Nsigs int, pubkeys []*btcec.PublicKey) bool {
	pubkeysCopy := make([]*btcec.PublicKey, len(pubkeys))
	copy(pubkeysCopy, pubkeys)

	validSignatures := 0
	for _, signature := range signatures {
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
