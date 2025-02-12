// Package cashu contains the core structs and logic
// of the Cashu protocol.
package cashu

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/fxamacker/cbor/v2"
)

type Unit int

const (
	Sat Unit = iota

	BOLT11_METHOD = "bolt11"
)

func (unit Unit) String() string {
	switch unit {
	case Sat:
		return "sat"
	default:
		return "unknown"
	}
}

var (
	ErrInvalidTokenV3 = errors.New("invalid V3 token")
	ErrInvalidTokenV4 = errors.New("invalid V4 token")
	ErrInvalidUnit    = errors.New("invalid unit")
)

// Cashu BlindedMessage. See https://github.com/cashubtc/nuts/blob/main/00.md#blindedmessage
type BlindedMessage struct {
	Amount  uint64 `json:"amount"`
	B_      string `json:"B_"`
	Id      string `json:"id"`
	Witness string `json:"witness,omitempty"`
}

func NewBlindedMessage(id string, amount uint64, B_ *secp256k1.PublicKey) BlindedMessage {
	B_str := hex.EncodeToString(B_.SerializeCompressed())
	return BlindedMessage{Amount: amount, B_: B_str, Id: id}
}

func SortBlindedMessages(blindedMessages BlindedMessages, secrets []string, rs []*secp256k1.PrivateKey) {
	// sort messages, secrets and rs
	for i := 0; i < len(blindedMessages)-1; i++ {
		for j := i + 1; j < len(blindedMessages); j++ {
			if blindedMessages[i].Amount > blindedMessages[j].Amount {
				// Swap blinded messages
				blindedMessages[i], blindedMessages[j] = blindedMessages[j], blindedMessages[i]

				// Swap secrets
				secrets[i], secrets[j] = secrets[j], secrets[i]

				// Swap rs
				rs[i], rs[j] = rs[j], rs[i]
			}
		}
	}
}

type BlindedMessages []BlindedMessage

func (bm BlindedMessages) Amount() uint64 {
	var totalAmount uint64 = 0
	for _, msg := range bm {
		totalAmount += msg.Amount
	}
	return totalAmount
}

// Cashu BlindedSignature. See https://github.com/cashubtc/nuts/blob/main/00.md#blindsignature
type BlindedSignature struct {
	Amount uint64 `json:"amount"`
	C_     string `json:"C_"`
	Id     string `json:"id"`
	// doing pointer here so that omitempty works.
	// an empty struct would still get marshalled
	DLEQ *DLEQProof `json:"dleq,omitempty"`
}

type BlindedSignatures []BlindedSignature

func (bs BlindedSignatures) Amount() uint64 {
	var totalAmount uint64 = 0
	for _, sig := range bs {
		totalAmount += sig.Amount
	}
	return totalAmount
}

// Cashu Proof. See https://github.com/cashubtc/nuts/blob/main/00.md#proof
type Proof struct {
	Amount  uint64 `json:"amount"`
	Id      string `json:"id"`
	Secret  string `json:"secret"`
	C       string `json:"C"`
	Witness string `json:"witness,omitempty"`
	// doing pointer here so that omitempty works.
	// an empty struct would still get marshalled
	DLEQ *DLEQProof `json:"dleq,omitempty"`
}

type Proofs []Proof

type DLEQProof struct {
	E string `json:"e"`
	S string `json:"s"`
	R string `json:"r,omitempty"`
}

// Amount returns the total amount from
// the array of Proof
func (proofs Proofs) Amount() uint64 {
	var totalAmount uint64 = 0
	for _, proof := range proofs {
		totalAmount += proof.Amount
	}
	return totalAmount
}

// Cashu token. See https://github.com/cashubtc/nuts/blob/main/00.md#token-format
type Token interface {
	Proofs() Proofs
	Mint() string
	Amount() uint64
	Serialize() (string, error)
}

func DecodeToken(tokenstr string) (Token, error) {
	token, err := DecodeTokenV4(tokenstr)
	if err != nil {
		// if err, try decoding as V3
		tokenV3, err := DecodeTokenV3(tokenstr)
		if err != nil {
			return nil, fmt.Errorf("invalid token: %v", err)
		}
		return tokenV3, nil
	}
	return token, nil
}

type TokenV3 struct {
	Token []TokenV3Proof `json:"token"`
	Unit  string         `json:"unit"`
	Memo  string         `json:"memo,omitempty"`
}

type TokenV3Proof struct {
	Mint   string `json:"mint"`
	Proofs Proofs `json:"proofs"`
}

func NewTokenV3(proofs Proofs, mint string, unit Unit, includeDLEQ bool) (TokenV3, error) {
	if !includeDLEQ {
		for i := 0; i < len(proofs); i++ {
			proofs[i].DLEQ = nil
		}
	}

	if unit != Sat {
		return TokenV3{}, ErrInvalidUnit
	}

	tokenProof := TokenV3Proof{Mint: mint, Proofs: proofs}
	return TokenV3{Token: []TokenV3Proof{tokenProof}, Unit: unit.String()}, nil
}

func DecodeTokenV3(tokenstr string) (*TokenV3, error) {
	prefixVersion := tokenstr[:6]
	base64Token := tokenstr[6:]

	if prefixVersion != "cashuA" {
		return nil, ErrInvalidTokenV3
	}

	tokenBytes, err := base64.URLEncoding.DecodeString(base64Token)
	if err != nil {
		tokenBytes, err = base64.RawURLEncoding.DecodeString(base64Token)
		if err != nil {
			return nil, fmt.Errorf("error decoding token: %v", err)
		}
	}

	var token TokenV3
	err = json.Unmarshal(tokenBytes, &token)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling token: %v", err)
	}

	return &token, nil
}

func (t TokenV3) Proofs() Proofs {
	proofs := make(Proofs, 0)
	for _, tokenProof := range t.Token {
		proofs = append(proofs, tokenProof.Proofs...)
	}
	return proofs
}

func (t TokenV3) Mint() string {
	return t.Token[0].Mint
}

func (t TokenV3) Amount() uint64 {
	var totalAmount uint64 = 0
	for _, tokenProof := range t.Token {
		for _, proof := range tokenProof.Proofs {
			totalAmount += proof.Amount
		}
	}
	return totalAmount
}

func (t TokenV3) Serialize() (string, error) {
	jsonBytes, err := json.Marshal(t)
	if err != nil {
		return "", err
	}

	token := "cashuA" + base64.URLEncoding.EncodeToString(jsonBytes)
	return token, nil
}

type TokenV4 struct {
	TokenProofs []TokenV4Proof `json:"t"`
	Memo        string         `json:"d,omitempty"`
	MintURL     string         `json:"m"`
	Unit        string         `json:"u"`
}

type TokenV4Proof struct {
	Id     []byte    `json:"i"`
	Proofs []ProofV4 `json:"p"`
}

func (tp *TokenV4Proof) MarshalJSON() ([]byte, error) {
	tokenProof := struct {
		Id     string    `json:"i"`
		Proofs []ProofV4 `json:"p"`
	}{
		Id:     hex.EncodeToString(tp.Id),
		Proofs: tp.Proofs,
	}
	return json.Marshal(tokenProof)
}

type ProofV4 struct {
	Amount  uint64  `json:"a"`
	Secret  string  `json:"s"`
	C       []byte  `json:"c"`
	Witness string  `json:"w,omitempty"`
	DLEQ    *DLEQV4 `json:"d,omitempty"`
}

func (p *ProofV4) MarshalJSON() ([]byte, error) {
	proof := struct {
		Amount  uint64  `json:"a"`
		Secret  string  `json:"s"`
		C       string  `json:"c"`
		Witness string  `json:"w,omitempty"`
		DLEQ    *DLEQV4 `json:"d,omitempty"`
	}{
		Amount:  p.Amount,
		Secret:  p.Secret,
		C:       hex.EncodeToString(p.C),
		Witness: p.Witness,
		DLEQ:    p.DLEQ,
	}
	return json.Marshal(proof)
}

type DLEQV4 struct {
	E []byte `json:"e"`
	S []byte `json:"s"`
	R []byte `json:"r"`
}

func (d *DLEQV4) MarshalJSON() ([]byte, error) {
	dleq := DLEQProof{
		E: hex.EncodeToString(d.E),
		S: hex.EncodeToString(d.S),
		R: hex.EncodeToString(d.R),
	}
	return json.Marshal(dleq)
}

func NewTokenV4(proofs Proofs, mint string, unit Unit, includeDLEQ bool) (TokenV4, error) {
	if unit != Sat {
		return TokenV4{}, ErrInvalidUnit
	}

	proofsMap := make(map[string][]ProofV4)
	for _, proof := range proofs {
		C, err := hex.DecodeString(proof.C)
		if err != nil {
			return TokenV4{}, fmt.Errorf("invalid C: %v", err)
		}
		proofV4 := ProofV4{
			Amount:  proof.Amount,
			Secret:  proof.Secret,
			C:       C,
			Witness: proof.Witness,
		}
		if includeDLEQ {
			if proof.DLEQ != nil {
				e, err := hex.DecodeString(proof.DLEQ.E)
				if err != nil {
					return TokenV4{}, fmt.Errorf("invalid e in DLEQ proof: %v", err)
				}
				s, err := hex.DecodeString(proof.DLEQ.S)
				if err != nil {
					return TokenV4{}, fmt.Errorf("invalid s in DLEQ proof: %v", err)
				}

				var r []byte
				if len(proof.DLEQ.R) > 0 {
					r, err = hex.DecodeString(proof.DLEQ.R)
					if err != nil {
						return TokenV4{}, fmt.Errorf("invalid r in DLEQ proof: %v", err)
					}
				} else {
					return TokenV4{}, errors.New("r in DLEQ proof cannot be empty")
				}

				dleq := &DLEQV4{
					E: e,
					S: s,
					R: r,
				}
				proofV4.DLEQ = dleq
			}
		}
		proofsMap[proof.Id] = append(proofsMap[proof.Id], proofV4)
	}

	proofsV4 := make([]TokenV4Proof, len(proofsMap))
	i := 0
	for k, v := range proofsMap {
		keysetIdBytes, err := hex.DecodeString(k)
		if err != nil {
			return TokenV4{}, fmt.Errorf("invalid keyset id: %v", err)
		}
		proofV4 := TokenV4Proof{Id: keysetIdBytes, Proofs: v}
		proofsV4[i] = proofV4
		i++
	}

	return TokenV4{MintURL: mint, Unit: unit.String(), TokenProofs: proofsV4}, nil
}

func DecodeTokenV4(tokenstr string) (*TokenV4, error) {
	prefixVersion := tokenstr[:6]
	base64Token := tokenstr[6:]
	if prefixVersion != "cashuB" {
		return nil, ErrInvalidTokenV4
	}

	tokenBytes, err := base64.URLEncoding.DecodeString(base64Token)
	if err != nil {
		tokenBytes, err = base64.RawURLEncoding.DecodeString(base64Token)
		if err != nil {
			return nil, fmt.Errorf("error decoding token: %v", err)
		}
	}

	var tokenV4 TokenV4
	err = cbor.Unmarshal(tokenBytes, &tokenV4)
	if err != nil {
		return nil, fmt.Errorf("cbor.Unmarshal: %v", err)
	}

	return &tokenV4, nil
}

func (t TokenV4) Proofs() Proofs {
	proofs := make(Proofs, 0)
	for _, tokenV4Proof := range t.TokenProofs {
		keysetId := hex.EncodeToString(tokenV4Proof.Id)
		for _, proofV4 := range tokenV4Proof.Proofs {
			proof := Proof{
				Amount:  proofV4.Amount,
				Id:      keysetId,
				Secret:  proofV4.Secret,
				C:       hex.EncodeToString(proofV4.C),
				Witness: proofV4.Witness,
			}
			if proofV4.DLEQ != nil {
				dleq := &DLEQProof{
					E: hex.EncodeToString(proofV4.DLEQ.E),
					S: hex.EncodeToString(proofV4.DLEQ.S),
					R: hex.EncodeToString(proofV4.DLEQ.R),
				}
				proof.DLEQ = dleq
			}
			proofs = append(proofs, proof)
		}
	}
	return proofs
}

func (t TokenV4) Mint() string {
	return t.MintURL
}

func (t TokenV4) Amount() uint64 {
	var totalAmount uint64
	proofs := t.Proofs()
	for _, proof := range proofs {
		totalAmount += proof.Amount
	}
	return totalAmount
}

func (t TokenV4) Serialize() (string, error) {
	cborData, err := cbor.Marshal(t)
	if err != nil {
		return "", err
	}

	token := "cashuB" + base64.RawURLEncoding.EncodeToString(cborData)
	return token, nil
}

type CashuErrCode int

// Error represents an error to be returned by the mint
type Error struct {
	Detail string       `json:"detail"`
	Code   CashuErrCode `json:"code"`
}

func BuildCashuError(detail string, code CashuErrCode) *Error {
	return &Error{Detail: detail, Code: code}
}

func (e Error) Error() string {
	return e.Detail
}

// Common error codes
const (
	StandardErrCode CashuErrCode = 10000
	// These will never be returned in a response.
	// Using them to identify internally where
	// the error originated and log appropriately
	DBErrCode               CashuErrCode = 1
	LightningBackendErrCode CashuErrCode = 2

	UnitErrCode                        CashuErrCode = 11005
	PaymentMethodErrCode               CashuErrCode = 11007
	BlindedMessageAlreadySignedErrCode CashuErrCode = 10002

	InvalidProofErrCode            CashuErrCode = 10003
	ProofAlreadyUsedErrCode        CashuErrCode = 11001
	InsufficientProofAmountErrCode CashuErrCode = 11002

	UnknownKeysetErrCode  CashuErrCode = 12001
	InactiveKeysetErrCode CashuErrCode = 12002

	AmountLimitExceeded            CashuErrCode = 11006
	MintQuoteRequestNotPaidErrCode CashuErrCode = 20001
	MintQuoteAlreadyIssuedErrCode  CashuErrCode = 20002
	MintingDisabledErrCode         CashuErrCode = 20003
	MintQuoteInvalidSigErrCode     CashuErrCode = 20008

	MeltQuotePendingErrCode     CashuErrCode = 20005
	MeltQuoteAlreadyPaidErrCode CashuErrCode = 20006

	//LightningPaymentErrCode     CashuErrCode = 20008
	MeltQuoteErrCode CashuErrCode = 20009
)

var (
	StandardErr                  = Error{Detail: "mint is currently unable to process request", Code: StandardErrCode}
	EmptyBodyErr                 = Error{Detail: "request body cannot be empty", Code: StandardErrCode}
	UnknownKeysetErr             = Error{Detail: "unknown keyset", Code: UnknownKeysetErrCode}
	PaymentMethodNotSupportedErr = Error{Detail: "payment method not supported", Code: PaymentMethodErrCode}
	UnitNotSupportedErr          = Error{Detail: "unit not supported", Code: UnitErrCode}
	InvalidBlindedMessageAmount  = Error{Detail: "invalid amount in blinded message", Code: StandardErrCode}
	BlindedMessageAlreadySigned  = Error{Detail: "blinded message already signed", Code: BlindedMessageAlreadySignedErrCode}
	MintQuoteRequestNotPaid      = Error{Detail: "quote request has not been paid", Code: MintQuoteRequestNotPaidErrCode}
	MintQuoteAlreadyIssued       = Error{Detail: "quote already issued", Code: MintQuoteAlreadyIssuedErrCode}
	MintingDisabled              = Error{Detail: "minting is disabled", Code: MintingDisabledErrCode}
	MintAmountExceededErr        = Error{Detail: "max amount for minting exceeded", Code: AmountLimitExceeded}
	MintQuoteInvalidSigErr       = Error{Detail: "Mint quote with pubkey but no valid signature provided.", Code: MintQuoteInvalidSigErrCode}
	OutputsOverQuoteAmountErr    = Error{Detail: "sum of the output amounts is greater than quote amount", Code: StandardErrCode}
	ProofAlreadyUsedErr          = Error{Detail: "proof already used", Code: ProofAlreadyUsedErrCode}
	ProofPendingErr              = Error{Detail: "proof is pending", Code: ProofAlreadyUsedErrCode}
	InvalidProofErr              = Error{Detail: "invalid proof", Code: InvalidProofErrCode}
	NoProofsProvided             = Error{Detail: "no proofs provided", Code: InvalidProofErrCode}
	DuplicateProofs              = Error{Detail: "duplicate proofs", Code: InvalidProofErrCode}
	QuoteNotExistErr             = Error{Detail: "quote does not exist", Code: MeltQuoteErrCode}
	QuotePending                 = Error{Detail: "quote is pending", Code: MeltQuotePendingErrCode}
	MeltQuoteAlreadyPaid         = Error{Detail: "quote already paid", Code: MeltQuoteAlreadyPaidErrCode}
	MeltAmountExceededErr        = Error{Detail: "max amount for melting exceeded", Code: AmountLimitExceeded}
	MeltQuoteForRequestExists    = Error{Detail: "melt quote for payment request already exists", Code: MeltQuoteErrCode}
	InsufficientProofsAmount     = Error{
		Detail: "amount of input proofs is below amount needed for transaction",
		Code:   InsufficientProofAmountErrCode,
	}
	InactiveKeysetSignatureRequest = Error{Detail: "requested signature from inactive keyset", Code: InactiveKeysetErrCode}
)

// Given an amount, it returns list of amounts e.g 13 -> [1, 4, 8]
// that can be used to build blinded messages or split operations.
// from nutshell implementation
func AmountSplit(amount uint64) []uint64 {
	rv := make([]uint64, 0)
	for pos := 0; amount > 0; pos++ {
		if amount&1 == 1 {
			rv = append(rv, 1<<pos)
		}
		amount >>= 1
	}
	return rv
}

func CheckDuplicateProofs(proofs Proofs) bool {
	proofsMap := make(map[Proof]bool)

	for _, proof := range proofs {
		if proofsMap[proof] {
			return true
		} else {
			proofsMap[proof] = true
		}
	}

	return false
}

func GenerateRandomQuoteId() (string, error) {
	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(randomBytes)
	return hex.EncodeToString(hash[:]), nil
}

func Max(x, y uint64) uint64 {
	if x > y {
		return x
	}
	return y
}

func Count(amounts []uint64, amount uint64) uint {
	var count uint = 0
	for _, amt := range amounts {
		if amt == amount {
			count++
		}
	}
	return count
}
