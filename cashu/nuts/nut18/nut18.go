package nut18

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/cashu/nuts/nut10"
	"github.com/fxamacker/cbor/v2"
)

const PaymentRequestPrefix = "creq"
const PaymentRequestV1 = "A"

type TransportTypes string

const Nostr TransportTypes = "nostr"
const Http TransportTypes = "http"

// const Nostr = "nostr"
const Rest = "rest"
const NIP17 = "17"
const NIP60 = "60"

var (
	ErrUnitNotSet = errors.New("You need to set the Unit when using amounts")
)

type PaymentRequest struct {
	Id          *string                `json:"i,omitempty" cbor:"i,omitempty"`
	Amount      *uint64                `json:"a,omitempty" cbor:"a,omitempty"`
	Unit        *string                `json:"u,omitempty" cbor:"u,omitempty"`
	Single      *bool                  `json:"s,omitempty" cbor:"s,omitempty"`
	Mints       []string              `json:"m,omitempty" cbor:"m,omitempty"`
	Description *string                `json:"d,omitempty" cbor:"d,omitempty"`
	Transport   []Transport           `json:"t,omitempty" cbor:"t,omitempty"`
	Nut10       *nut10.WellKnownSecret `json:"nut10,omitempty" cbor:"nut10,omitempty"`
}

type Transport struct {
	Type   TransportTypes `json:"t" cbor:"t"`
	Target string         `json:"a" cbor:"a"`
	Tags   [][]string     `json:"g,omitempty" cbor:"g,omitempty"`
}

func (p PaymentRequest) Encode() (string, error) {
	tokenBytes, err := cbor.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("cbor.Marshal(p): %w", err)
	}

	return PaymentRequestPrefix + PaymentRequestV1 + base64.URLEncoding.EncodeToString(tokenBytes), nil
}

func (p *PaymentRequest) AddAmount(amount uint64, unit string) error {
	if unit == "" {
		return ErrUnitNotSet
	}

	p.Amount = &amount
	p.Unit = &unit

	return nil
}
func (p *PaymentRequest) SetSingleUse() {
	single := true
	p.Single = &single
}

func (p *PaymentRequest) SetMints(mints []string) {
	p.Mints = mints
}

func (p *PaymentRequest) SetDescription(desc string) {
	p.Description = &desc
}

func (p *PaymentRequest) SetNostr(nprofile string) {
	transportTags := [][]string{
		{"n", NIP17},
		{"n", NIP60},
	}
	transport := Transport{
		Type:   Nostr,
		Target: nprofile,
		Tags:   transportTags,
	}
	p.Transport = append(p.Transport, transport)
}

func DecodePaymentRequest(requestString string) (PaymentRequest, error) {
	if len(requestString) < len(PaymentRequestPrefix)+len(PaymentRequestV1) {
		return PaymentRequest{}, fmt.Errorf("payment request is too small")
	}
	encodedToken := requestString[len(PaymentRequestPrefix)+len(PaymentRequestV1):]
	log.Println("encodedToken: ", encodedToken)
	base64DecodedToken, err := base64.URLEncoding.DecodeString(encodedToken)
	if err != nil {
		return PaymentRequest{}, fmt.Errorf("base64.URLEncoding.DecodeString(encodedToken): %w", err)
	}

	var payReq PaymentRequest
	err = cbor.Unmarshal(base64DecodedToken, &payReq)
	if err != nil {
		return PaymentRequest{}, fmt.Errorf("cbor.Marshal(p): %v", err)
	}

	return payReq, nil
}

type PaymentRequestPayload struct {
	Id     string       `json:"id,omitempty"`
	Memo   string       `json:"memo,omitempty"`
	Mint   string       `json:"mint"`
	Unit   string       `json:"unit"`
	Proofs cashu.Proofs `json:"proofs"`
}
