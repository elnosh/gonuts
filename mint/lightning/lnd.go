package lightning

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	InvoiceExpiryMins         = 10
	FeePercent        float64 = 0.01
)

type LndConfig struct {
	GRPCHost string
	Cert     credentials.TransportCredentials
	Macaroon macaroons.MacaroonCredential
}

type LndClient struct {
	grpcClient lnrpc.LightningClient
}

func SetupLndClient(config LndConfig) (*LndClient, error) {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(config.Cert),
		grpc.WithPerRPCCredentials(config.Macaroon),
	}

	conn, err := grpc.NewClient(config.GRPCHost, opts...)
	if err != nil {
		return nil, fmt.Errorf("error setting up grpc client: %v", err)
	}

	grpcClient := lnrpc.NewLightningClient(conn)
	return &LndClient{grpcClient: grpcClient}, nil
}

func (lnd *LndClient) CreateInvoice(amount uint64) (Invoice, error) {
	invoiceRequest := lnrpc.Invoice{
		Value:  int64(amount),
		Expiry: InvoiceExpiryMins * 60,
	}

	addInvoiceResponse, err := lnd.grpcClient.AddInvoice(context.Background(), &invoiceRequest)
	if err != nil {
		return Invoice{}, fmt.Errorf("could not generate invoice: %v", err)
	}
	hash := hex.EncodeToString(addInvoiceResponse.RHash)

	invoice := Invoice{
		PaymentRequest: addInvoiceResponse.PaymentRequest,
		PaymentHash:    hash,
		Amount:         amount,
		Expiry:         uint64(time.Now().Add(time.Minute * InvoiceExpiryMins).Unix()),
	}
	return invoice, nil
}

func (lnd *LndClient) InvoiceStatus(hash string) (Invoice, error) {
	hashBytes, err := hex.DecodeString(hash)
	if err != nil {
		return Invoice{}, errors.New("invalid hash provided")
	}

	paymentHashRequest := lnrpc.PaymentHash{RHash: hashBytes}
	lookupInvoiceResponse, err := lnd.grpcClient.LookupInvoice(context.Background(), &paymentHashRequest)
	if err != nil {
		return Invoice{}, err
	}

	invoiceSettled := lookupInvoiceResponse.State == lnrpc.Invoice_SETTLED
	invoice := Invoice{
		PaymentRequest: lookupInvoiceResponse.PaymentRequest,
		PaymentHash:    hash,
		Preimage:       hex.EncodeToString(lookupInvoiceResponse.RPreimage),
		Settled:        invoiceSettled,
		Amount:         uint64(lookupInvoiceResponse.Value),
	}

	return invoice, nil
}

type SendPaymentResponse struct {
	PaymentError    string `json:"payment_error"`
	PaymentPreimage string `json:"payment_preimage"`
}

func (lnd *LndClient) SendPayment(request string, amount uint64) (string, error) {
	feeLimit := lnd.FeeReserve(amount)
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: request,
		FeeLimit:       &lnrpc.FeeLimit{Limit: &lnrpc.FeeLimit_Fixed{Fixed: int64(feeLimit)}},
	}

	sendPaymentResponse, err := lnd.grpcClient.SendPaymentSync(context.Background(), &sendPaymentRequest)
	if err != nil {
		return "", fmt.Errorf("error making payment: %v", err)
	}
	if len(sendPaymentResponse.PaymentError) > 0 {
		return "", fmt.Errorf("error making payment: %v", sendPaymentResponse.PaymentError)
	}
	if len(sendPaymentResponse.PaymentPreimage) == 0 {
		return "", fmt.Errorf("could not make payment")
	}

	preimage := hex.EncodeToString(sendPaymentResponse.PaymentPreimage)
	return preimage, nil
}

func (lnd *LndClient) FeeReserve(amount uint64) uint64 {
	fee := math.Ceil(float64(amount) * FeePercent)
	return uint64(fee)
}
