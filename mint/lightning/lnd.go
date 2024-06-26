package lightning

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

const (
	LND_GRPC_HOST     = "LND_GRPC_HOST"
	LND_CERT_PATH     = "LND_CERT_PATH"
	LND_MACAROON_PATH = "LND_MACAROON_PATH"
)

const (
	InvoiceExpiryMins         = 10
	FeePercent        float64 = 0.01
)

type LndClient struct {
	grpcClient lnrpc.LightningClient
}

func CreateLndClient() (*LndClient, error) {
	host := os.Getenv(LND_GRPC_HOST)
	if host == "" {
		return nil, errors.New(LND_GRPC_HOST + " cannot be empty")
	}
	certPath := os.Getenv(LND_CERT_PATH)
	if certPath == "" {
		return nil, errors.New(LND_CERT_PATH + " cannot be empty")
	}
	macaroonPath := os.Getenv(LND_MACAROON_PATH)
	if macaroonPath == "" {
		return nil, errors.New(LND_MACAROON_PATH + " cannot be empty")
	}

	creds, err := credentials.NewClientTLSFromFile(certPath, "")
	if err != nil {
		return nil, err
	}

	macaroonBytes, err := os.ReadFile(macaroonPath)
	if err != nil {
		return nil, fmt.Errorf("error reading macaroon: os.ReadFile %v", err)
	}

	macaroon := &macaroon.Macaroon{}
	if err = macaroon.UnmarshalBinary(macaroonBytes); err != nil {
		return nil, fmt.Errorf("unable to decode macaroon: %v", err)
	}
	macarooncreds, err := macaroons.NewMacaroonCredential(macaroon)
	if err != nil {
		return nil, fmt.Errorf("error setting macaroon creds: %v", err)
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(macarooncreds),
	}

	conn, err := grpc.NewClient(host, opts...)
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
		Expiry:         time.Now().Add(time.Minute * InvoiceExpiryMins).Unix(),
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
		Settled:        invoiceSettled,
		Amount:         uint64(lookupInvoiceResponse.Value),
	}

	if invoiceSettled {
		preimage := hex.EncodeToString(lookupInvoiceResponse.RPreimage)
		invoice.Preimage = preimage
	}

	return invoice, nil
}

func (lnd *LndClient) FeeReserve(amount uint64) uint64 {
	fee := math.Ceil(float64(amount) * FeePercent)
	return uint64(fee)
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
