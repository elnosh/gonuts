package nut17

import (
	"encoding/json"
	"errors"
)

type SubscriptionKind int

const (
	Bolt11MintQuote SubscriptionKind = iota
	Bolt11MeltQuote
	ProofState
	Unknown
)

const (
	JSONRPC_2   = "2.0"
	OK          = "OK"
	SUBSCRIBE   = "subscribe"
	UNSUBSCRIBE = "unsubscribe"
)

func (kind SubscriptionKind) String() string {
	switch kind {
	case Bolt11MintQuote:
		return "bolt11_mint_quote"
	case Bolt11MeltQuote:
		return "bolt11_melt_quote"
	case ProofState:
		return "proof_state"
	default:
		return "unknown"
	}
}

func StringToKind(kind string) SubscriptionKind {
	switch kind {
	case "bolt11_mint_quote":
		return Bolt11MintQuote
	case "bolt11_melt_quote":
		return Bolt11MeltQuote
	case "proof_state":
		return ProofState
	}
	return Unknown
}

type WsRequest struct {
	JsonRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  RequestParams `json:"params"`
	Id      int           `json:"id"`
}

type RequestParams struct {
	Kind    string   `json:"kind"`
	SubId   string   `json:"subId"`
	Filters []string `json:"filters"`
}

type WsResponse struct {
	JsonRPC string `json:"jsonrpc"`
	Result  Result `json:"result"`
	Id      int    `json:"id"`
}

func (r *WsResponse) UnmarshalJSON(data []byte) error {
	var tempResponse struct {
		JsonRPC string  `json:"jsonrpc"`
		Result  *Result `json:"result"`
		Id      int     `json:"id"`
	}

	if err := json.Unmarshal(data, &tempResponse); err != nil {
		return err
	}

	if tempResponse.Result == nil {
		return errors.New("result field not present in WsResponse")
	}

	r.JsonRPC = tempResponse.JsonRPC
	r.Result = *tempResponse.Result
	r.Id = tempResponse.Id

	return nil
}

type Result struct {
	Status string `json:"status"`
	SubId  string `json:"subId"`
}

type WsNotification struct {
	JsonRPC string             `json:"jsonrpc"`
	Method  string             `json:"method"`
	Params  NotificationParams `json:"params"`
}

func (n *WsNotification) UnmarshalJSON(data []byte) error {
	var tempNotif struct {
		JsonRPC string              `json:"jsonrpc"`
		Method  string              `json:"method"`
		Params  *NotificationParams `json:"params"`
	}

	if err := json.Unmarshal(data, &tempNotif); err != nil {
		return err
	}

	if tempNotif.Params == nil {
		return errors.New("params field not present in WsNotification")
	}

	n.JsonRPC = tempNotif.JsonRPC
	n.Method = tempNotif.Method
	n.Params = *tempNotif.Params

	return nil
}

type NotificationParams struct {
	SubId   string          `json:"subId"`
	Payload json.RawMessage `json:"payload"`
}

type WsError struct {
	JsonRPC     string        `json:"jsonrpc"`
	ErrResponse ErrorResponse `json:"error"`
	Id          int           `json:"id"`
}

func NewWsError(code int, message string, id int) WsError {
	return WsError{
		JsonRPC: JSONRPC_2,
		ErrResponse: ErrorResponse{
			Code:    code,
			Message: message,
		},
		Id: id,
	}
}

func (e *WsError) UnmarshalJSON(data []byte) error {
	var tempError struct {
		JsonRPC     string         `json:"jsonrpc"`
		ErrResponse *ErrorResponse `json:"error"`
		Id          int            `json:"id"`
	}

	if err := json.Unmarshal(data, &tempError); err != nil {
		return err
	}

	if tempError.ErrResponse == nil {
		return errors.New("error field not present in WsError")
	}

	e.JsonRPC = tempError.JsonRPC
	e.ErrResponse = *tempError.ErrResponse
	e.Id = tempError.Id

	return nil
}

func (e WsError) Error() string {
	return e.ErrResponse.Message
}

type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type InfoSetting struct {
	Supported []SupportedMethod `json:"supported"`
}

type SupportedMethod struct {
	Method   string   `json:"method"`
	Unit     string   `json:"unit"`
	Commands []string `json:"commands"`
}
