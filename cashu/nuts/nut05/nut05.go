// Package nut05 contains structs as defined in [NUT-05]
//
// [NUT-05]: https://github.com/cashubtc/nuts/blob/main/05.md
package nut05

import (
	"encoding/json"

	"github.com/elnosh/gonuts/cashu"
)

type State int

const (
	Unpaid State = iota
	Pending
	Paid
	Unknown
)

func (state State) String() string {
	switch state {
	case Unpaid:
		return "UNPAID"
	case Pending:
		return "PENDING"
	case Paid:
		return "PAID"
	default:
		return "unknown"
	}
}

func StringToState(state string) State {
	switch state {
	case "UNPAID":
		return Unpaid
	case "PENDING":
		return Pending
	case "PAID":
		return Paid
	}
	return Unknown
}

type PostMeltQuoteBolt11Request struct {
	Request string `json:"request"`
	Unit    string `json:"unit"`
}

type PostMeltQuoteBolt11Response struct {
	Quote      string `json:"quote"`
	Amount     uint64 `json:"amount"`
	FeeReserve uint64 `json:"fee_reserve"`
	State      State  `json:"state"`
	Paid       bool   `json:"paid"` // DEPRECATED: use state instead
	Expiry     int64  `json:"expiry"`
	Preimage   string `json:"payment_preimage,omitempty"`
}

type PostMeltBolt11Request struct {
	Quote  string       `json:"quote"`
	Inputs cashu.Proofs `json:"inputs"`
}

type TempQuote struct {
	Quote      string `json:"quote"`
	Amount     uint64 `json:"amount"`
	FeeReserve uint64 `json:"fee_reserve"`
	State      string `json:"state"`
	Paid       bool   `json:"paid"` // DEPRECATED: use state instead
	Expiry     int64  `json:"expiry"`
	Preimage   string `json:"payment_preimage,omitempty"`
}

func (quoteResponse *PostMeltQuoteBolt11Response) MarshalJSON() ([]byte, error) {
	var tempQuote = TempQuote{
		Quote:      quoteResponse.Quote,
		Amount:     quoteResponse.Amount,
		FeeReserve: quoteResponse.FeeReserve,
		State:      quoteResponse.State.String(),
		Paid:       quoteResponse.Paid,
		Expiry:     quoteResponse.Expiry,
		Preimage:   quoteResponse.Preimage,
	}
	return json.Marshal(tempQuote)
}

func (quoteResponse *PostMeltQuoteBolt11Response) UnmarshalJSON(data []byte) error {
	tempQuote := &TempQuote{}

	if err := json.Unmarshal(data, tempQuote); err != nil {
		return err
	}

	quoteResponse.Quote = tempQuote.Quote
	quoteResponse.Amount = tempQuote.Amount
	quoteResponse.FeeReserve = tempQuote.FeeReserve
	state := StringToState(tempQuote.State)
	quoteResponse.State = state
	quoteResponse.Paid = tempQuote.Paid
	quoteResponse.Expiry = tempQuote.Expiry
	quoteResponse.Preimage = tempQuote.Preimage

	return nil
}
