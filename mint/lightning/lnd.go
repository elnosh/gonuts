package lightning

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
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

func (lnd *LndClient) ConnectionStatus() error {
	// call to check connection is good
	request := lnrpc.WalletBalanceRequest{}
	_, err := lnd.grpcClient.WalletBalance(context.Background(), &request)
	if err != nil {
		return err
	}
	return nil
}

func (lnd *LndClient) CreateInvoice(amount uint64) (Invoice, error) {
	invoiceRequest := lnrpc.Invoice{
		Value:  int64(amount),
		Expiry: InvoiceExpiryMins * 60,
	}

	addInvoiceResponse, err := lnd.grpcClient.AddInvoice(context.Background(), &invoiceRequest)
	if err != nil {
		return Invoice{}, err
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
	feeReserve := lnd.FeeReserve(amount)
	feeLimit := lnrpc.FeeLimit{Limit: &lnrpc.FeeLimit_Fixed{Fixed: int64(feeReserve)}}

	// if amount is less than amount in invoice, pay partially if supported by backend.
	// not checking err because invoice has already been validated by the mint
	req := lnrpc.PayReqString{PayReq: request}
	payReq, err := lnd.grpcClient.DecodePayReq(ctx, &req)
	if err != nil {
		return PaymentStatus{PaymentStatus: Failed}, err
	}
	if amount < uint64(payReq.NumMsat) {
		return lnd.payPartialInvoice(ctx, payReq, amount, &feeLimit)
	}

	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: request,
		FeeLimit:       &feeLimit,
	}
	sendPaymentResponse, err := lnd.grpcClient.SendPaymentSync(ctx, &sendPaymentRequest)
	if err != nil {
		// if context deadline is exceeded (1 min), mark payment as pending
		// if any other error, mark as failed
		if errors.Is(ctx.Err(), context.DeadlineExceeded) ||
			strings.Contains(err.Error(), "context deadline exceeded") {
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

func (lnd *LndClient) payPartialInvoice(
	ctx context.Context,
	req *lnrpc.PayReq,
	partialAmountToPay uint64,
	feeLimit *lnrpc.FeeLimit,
) (PaymentStatus, error) {
	queryRoutesRequest := lnrpc.QueryRoutesRequest{
		PubKey:   req.Destination,
		Amt:      int64(partialAmountToPay),
		FeeLimit: feeLimit,
	}

	queryRoutesResponse, err := lnd.grpcClient.QueryRoutes(ctx, &queryRoutesRequest)
	if err != nil {
		return PaymentStatus{PaymentStatus: Failed}, err
	}
	if len(queryRoutesResponse.Routes) < 1 {
		return PaymentStatus{PaymentStatus: Failed}, errors.New("no routes found")
	}

	route := queryRoutesResponse.Routes[0]
	mppRecord := lnrpc.MPPRecord{PaymentAddr: req.PaymentAddr, TotalAmtMsat: req.NumMsat}
	route.Hops[len(route.Hops)-1].MppRecord = &mppRecord

	paymentHashBytes, err := hex.DecodeString(req.PaymentHash)
	if err != nil {
		return PaymentStatus{PaymentStatus: Failed}, err
	}
	sendToRouteRequest := routerrpc.SendToRouteRequest{
		PaymentHash: paymentHashBytes,
		Route:       route,
		SkipTempErr: true,
	}

	htlcAttempt, err := lnd.routerClient.SendToRouteV2(ctx, &sendToRouteRequest)
	if err != nil {
		return PaymentStatus{PaymentStatus: Failed}, err
	}

	switch htlcAttempt.Status {
	case lnrpc.HTLCAttempt_SUCCEEDED:
		preimage := hex.EncodeToString(htlcAttempt.Preimage)
		paymentResponse := PaymentStatus{Preimage: preimage, PaymentStatus: Succeeded}
		return paymentResponse, nil
	case lnrpc.HTLCAttempt_FAILED:
		err := "payment failed"
		if htlcAttempt.Failure != nil {
			err = htlcAttempt.Failure.String()
		}
		return PaymentStatus{PaymentStatus: Failed}, errors.New(err)
	case lnrpc.HTLCAttempt_IN_FLIGHT:
		return PaymentStatus{PaymentStatus: Pending}, nil
	}

	return PaymentStatus{PaymentStatus: Failed}, errors.New("payment failed")
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
		if errors.Is(ctx.Err(), context.DeadlineExceeded) ||
			strings.Contains(err.Error(), "context deadline exceeded") {
			return PaymentStatus{PaymentStatus: Pending}, nil
		}
		return PaymentStatus{PaymentStatus: Failed}, err
	}

	// this should block until final payment update
	payment, err := trackPaymentStream.Recv()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) ||
			strings.Contains(err.Error(), "context deadline exceeded") {
			return PaymentStatus{PaymentStatus: Pending}, nil
		}
		return PaymentStatus{PaymentStatus: Failed}, err
	}
	if payment.Status == lnrpc.Payment_UNKNOWN || payment.Status == lnrpc.Payment_FAILED {
		return PaymentStatus{PaymentStatus: Failed, PaymentFailureReason: payment.FailureReason.String()}, nil
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
