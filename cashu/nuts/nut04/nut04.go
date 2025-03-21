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
	Pending
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
	case Pending:
		return "PENDING"
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
	case "PENDING":
		return Pending
	}
	return Unknown
}

type PostMintQuoteBolt11Request struct {
	Amount uint64 `json:"amount"`
	Unit   string `json:"unit"`
	Pubkey string `json:"pubkey,omitempty"`
}

type PostMintQuoteBolt11Response struct {
	Quote   string `json:"quote"`
	Request string `json:"request"`
	Amount  uint64 `json:"amount"`
	Unit    string `json:"unit"`
	State   State  `json:"state"`
	Expiry  uint64 `json:"expiry"`
	Pubkey  string `json:"pubkey,omitempty"`
}

type PostMintBolt11Request struct {
	Quote     string                `json:"quote"`
	Outputs   cashu.BlindedMessages `json:"outputs"`
	Signature string                `json:"signature,omitempty"`
}

type PostMintBolt11Response struct {
	Signatures cashu.BlindedSignatures `json:"signatures"`
}

type tempQuote struct {
	Quote   string `json:"quote"`
	Request string `json:"request"`
	Amount  uint64 `json:"amount"`
	Unit    string `json:"unit"`
	State   string `json:"state"`
	Expiry  uint64 `json:"expiry"`
	Pubkey  string `json:"pubkey,omitempty"`
}

func (quoteResponse *PostMintQuoteBolt11Response) MarshalJSON() ([]byte, error) {
	var tempQuote = tempQuote{
		Quote:   quoteResponse.Quote,
		Request: quoteResponse.Request,
		Amount:  quoteResponse.Amount,
		Unit:    quoteResponse.Unit,
		State:   quoteResponse.State.String(),
		Expiry:  quoteResponse.Expiry,
		Pubkey:  quoteResponse.Pubkey,
	}
	return json.Marshal(tempQuote)
}

func (quoteResponse *PostMintQuoteBolt11Response) UnmarshalJSON(data []byte) error {
	tempQuote := &tempQuote{}

	if err := json.Unmarshal(data, tempQuote); err != nil {
		return err
	}

	quoteResponse.Quote = tempQuote.Quote
	quoteResponse.Request = tempQuote.Request
	quoteResponse.Amount = tempQuote.Amount
	quoteResponse.Unit = tempQuote.Unit
	state := StringToState(tempQuote.State)
	quoteResponse.State = state
	quoteResponse.Expiry = tempQuote.Expiry
	quoteResponse.Pubkey = tempQuote.Pubkey

	return nil
}
