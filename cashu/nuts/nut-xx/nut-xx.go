package nutxx

import (
	"encoding/base64"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

const PaymentRequestPrefix = "creq"
const PaymentRequestV1 = "A"

type PaymentRequest struct {
 	I       string   `json:"i,omitempty", cbor:"i,omitempty"`
 	A       int   `json:"a,omitempty", cbor:"a,omitempty"`
 	U       int   `json:"u,omitempty", cbor:"u,omitempty"`
 	R       bool   `json:"r,omitempty", cbor:"r,omitempty"`
 	M       []string   `json:"m,omitempty", cbor:"m,omitempty"`
 	D       string   `json:"d,omitempty", cbor:"d,omitempty"`
 	T       []Transport   `json:"t", cbor:"t"`
}

type Transport struct {
 	T       string   `json:"t", cbor:"t"`
 	A       string   `json:"a", cbor:"a"`
 	G       [][]string   `json:"g", cbor:"g"`
}


func (p PaymentRequest) Encode() (string ,error){




	tokenBytes, err := cbor.Marshal(p)
	if err != nil {
			return "", fmt.Errorf("cbor.Marshal(p): %v", err)
	}

    return PaymentRequestPrefix + PaymentRequestV1 + base64.URLEncoding.EncodeToString(tokenBytes), nil

}
