// Package nut04 contains structs as defined in [NUT-04]
//
// [NUT-04]: https://github.com/cashubtc/nuts/blob/main/04.md
package nut04

import (
	"encoding/json"

	"github.com/elnosh/gonuts/cashu"
)

type State int

const (
	Unpaid State = iota
	Paid
	Issued
	Unknown
)

func (state State) String() string {
	switch state {
	case Unpaid:
		return "UNPAID"
	case Paid:
		return "PAID"
	case Issued:
		return "ISSUED"
	default:
		return "unknown"
	}
}

func StringToState(state string) State {
	switch state {
	case "UNPAID":
		return Unpaid
	case "PAID":
		return Paid
	case "ISSUED":
		return Issued
	}
	return Unknown
}

type PostMintQuoteBolt11Request struct {
	Amount uint64 `json:"amount"`
	Unit   string `json:"unit"`
}

type PostMintQuoteBolt11Response struct {
	Quote   string `json:"quote"`
	Request string `json:"request"`
	State   State  `json:"state"`
	Paid    bool   `json:"paid"` // DEPRECATED: use State instead
	Expiry  int64  `json:"expiry"`
}

type PostMintBolt11Request struct {
	Quote   string                `json:"quote"`
	Outputs cashu.BlindedMessages `json:"outputs"`
}

type PostMintBolt11Response struct {
	Signatures cashu.BlindedSignatures `json:"signatures"`
}

type TempQuote struct {
	Quote   string `json:"quote"`
	Request string `json:"request"`
	State   string `json:"state"`
	Paid    bool   `json:"paid"` // DEPRECATED: use State instead
	Expiry  int64  `json:"expiry"`
}

func (quoteResponse *PostMintQuoteBolt11Response) MarshalJSON() ([]byte, error) {
	var tempQuote = TempQuote{
		Quote:   quoteResponse.Quote,
		Request: quoteResponse.Request,
		State:   quoteResponse.State.String(),
		Paid:    quoteResponse.Paid,
		Expiry:  quoteResponse.Expiry,
	}
	return json.Marshal(tempQuote)
}

func (quoteResponse *PostMintQuoteBolt11Response) UnmarshalJSON(data []byte) error {
	tempQuote := &TempQuote{}

	if err := json.Unmarshal(data, tempQuote); err != nil {
		return err
	}

	quoteResponse.Quote = tempQuote.Quote
	quoteResponse.Request = tempQuote.Request
	state := StringToState(tempQuote.State)
	quoteResponse.State = state
	quoteResponse.Paid = tempQuote.Paid
	quoteResponse.Expiry = tempQuote.Expiry

	return nil
}
