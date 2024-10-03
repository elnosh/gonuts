package lightning

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/routerrpc"
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
	grpcClient   lnrpc.LightningClient
	routerClient routerrpc.RouterClient
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
	routerClient := routerrpc.NewRouterClient(conn)
	return &LndClient{grpcClient: grpcClient, routerClient: routerClient}, nil
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

func (lnd *LndClient) SendPayment(ctx context.Context, request string, amount uint64) (PaymentStatus, error) {
	feeLimit := lnd.FeeReserve(amount)
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: request,
		FeeLimit:       &lnrpc.FeeLimit{Limit: &lnrpc.FeeLimit_Fixed{Fixed: int64(feeLimit)}},
	}

	sendPaymentResponse, err := lnd.grpcClient.SendPaymentSync(ctx, &sendPaymentRequest)
	if err != nil {
		// if context deadline is exceeded (1 min), mark payment as pending
		// if any other error, mark as failed
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return PaymentStatus{PaymentStatus: Pending}, nil
		} else {
			return PaymentStatus{PaymentStatus: Failed}, err
		}
	}
	if len(sendPaymentResponse.PaymentError) > 0 {
		return PaymentStatus{PaymentStatus: Failed}, fmt.Errorf("payment error: %v", sendPaymentResponse.PaymentError)
	}

	preimage := hex.EncodeToString(sendPaymentResponse.PaymentPreimage)
	paymentResponse := PaymentStatus{Preimage: preimage, PaymentStatus: Succeeded}
	return paymentResponse, nil
}

func (lnd *LndClient) OutgoingPaymentStatus(ctx context.Context, hash string) (PaymentStatus, error) {
	hashBytes, err := hex.DecodeString(hash)
	if err != nil {
		return PaymentStatus{}, errors.New("invalid hash provided")
	}

	trackPaymentRequest := routerrpc.TrackPaymentRequest{
		PaymentHash: hashBytes,
		// setting this to only get the final payment update
		NoInflightUpdates: true,
	}

	trackPaymentStream, err := lnd.routerClient.TrackPaymentV2(ctx, &trackPaymentRequest)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return PaymentStatus{PaymentStatus: Pending}, nil
		}
		return PaymentStatus{PaymentStatus: Failed}, err
	}

	// this should block until final payment update
	payment, err := trackPaymentStream.Recv()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return PaymentStatus{PaymentStatus: Pending}, nil
		}
		return PaymentStatus{PaymentStatus: Failed}, fmt.Errorf("payment failed: %w", err)
	}
	if payment.Status == lnrpc.Payment_UNKNOWN || payment.Status == lnrpc.Payment_FAILED {
		return PaymentStatus{PaymentStatus: Failed},
			fmt.Errorf("payment failed: %s", payment.FailureReason.String())
	}
	if payment.Status == lnrpc.Payment_IN_FLIGHT {
		return PaymentStatus{PaymentStatus: Pending}, nil
	}
	if payment.Status == lnrpc.Payment_SUCCEEDED {
		return PaymentStatus{PaymentStatus: Succeeded, Preimage: payment.PaymentPreimage}, nil
	}

	return PaymentStatus{PaymentStatus: Failed}, errors.New("unknown")
}

func (lnd *LndClient) FeeReserve(amount uint64) uint64 {
	fee := math.Ceil(float64(amount) * FeePercent)
	return uint64(fee)
}
