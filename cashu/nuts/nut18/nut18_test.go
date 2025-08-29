package nut18

import (
	"fmt"
	"testing"
)

func TestDecodingPaymentReq(t *testing.T) {

	encodedPayReq := "creqApmF0gaNhdGVub3N0cmFheKlucHJvZmlsZTFxeTI4d3VtbjhnaGo3dW45ZDNzaGp0bnl2OWtoMnVld2Q5aHN6OW1od2RlbjV0ZTB3ZmprY2N0ZTljdXJ4dmVuOWVlaHFjdHJ2NWhzenJ0aHdkZW41dGUwZGVoaHh0bnZkYWtxcWd5dnB6NXlzajBkcXgzZHpwdjg1eHdscmFwZncwOTR3c3EwdDdkeHd6cHl6eXAwem0zMGd1dWV6Zng1YWeBgmExZk5JUC0wNGFpanBheW1lbnRfaWRhYQ1hdWNzYXRhbYF4Imh0dHBzOi8vbm9mZWVzLnRlc3RudXQuY2FzaHUuc3BhY2VhZHB0aGlzIGlzIHRoZSBtZW1v"

	payReq, err := DecodePaymentRequest(encodedPayReq)

	if err != nil {
		t.Fatalf("DecodePaymentRequest(encodedPayReq) %+v", err)
	}

	fmt.Printf("payment req: %+v", payReq)
}

// NUT-18 Test Vectors
func TestBasicPaymentRequest(t *testing.T) {
	id := "b7a90176"
	amt := uint64(10)
	unit := "sat"
	paymentRequest := PaymentRequest{
		Id:     &id,
		Amount: &amt,
		Unit:   &unit,
		Mints:  []string{"https://8333.space:3338"},
		Transport: []Transport{
			{
				Type:   "nostr",
				Target: "nprofile1qy28wumn8ghj7un9d3shjtnyv9kh2uewd9hsz9mhwden5te0wfjkccte9curxven9eehqctrv5hszrthwden5te0dehhxtnvdakqqgydaqy7curk439ykptkysv7udhdhu68sucm295akqefdehkf0d495cwunl5",
				Tags:   [][]string{{"n", "17"}},
			},
		},
	}

	newPaymentRequest, err := DecodePaymentRequest("creqApWF0gaNhdGVub3N0cmFheKlucHJvZmlsZTFxeTI4d3VtbjhnaGo3dW45ZDNzaGp0bnl2OWtoMnVld2Q5aHN6OW1od2RlbjV0ZTB3ZmprY2N0ZTljdXJ4dmVuOWVlaHFjdHJ2NWhzenJ0aHdkZW41dGUwZGVoaHh0bnZkYWtxcWd5ZGFxeTdjdXJrNDM5eWtwdGt5c3Y3dWRoZGh1NjhzdWNtMjk1YWtxZWZkZWhrZjBkNDk1Y3d1bmw1YWeBgmFuYjE3YWloYjdhOTAxNzZhYQphdWNzYXRhbYF3aHR0cHM6Ly84MzMzLnNwYWNlOjMzMzg=")
	if err != nil {
		t.Errorf("could not decode paymentRequest1. %+v", err)
	}

	if *newPaymentRequest.Id != *paymentRequest.Id {
		t.Errorf("payment request are not the same")
	}
	if *newPaymentRequest.Amount != *paymentRequest.Amount {
		t.Errorf("amount is not the same")
	}
	if *newPaymentRequest.Unit != *paymentRequest.Unit {
		t.Errorf("unit is not the same")
	}
	if newPaymentRequest.Mints[0] != paymentRequest.Mints[0] {
		t.Errorf("mints are not the same")
	}

}
