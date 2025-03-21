package lightning

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/invoicesrpc"
	"github.com/lightningnetwork/lnd/lnrpc/routerrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	decodepay "github.com/nbd-wtf/ln-decodepay"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	// 1 hour
	InvoiceExpiryTime         = 3600
	FeePercent        float64 = 0.01
)

type LndConfig struct {
	GRPCHost string
	Cert     credentials.TransportCredentials
	Macaroon macaroons.MacaroonCredential
}

type LndClient struct {
	grpcClient     lnrpc.LightningClient
	routerClient   routerrpc.RouterClient
	invoicesClient invoicesrpc.InvoicesClient
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
	invoicesClient := invoicesrpc.NewInvoicesClient(conn)

	return &LndClient{
		grpcClient:     grpcClient,
		routerClient:   routerClient,
		invoicesClient: invoicesClient,
	}, nil
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
		Expiry: InvoiceExpiryTime,
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
		Expiry:         InvoiceExpiryTime,
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
		Expiry:         uint64(lookupInvoiceResponse.Expiry),
	}

	return invoice, nil
}

func (lnd *LndClient) SendPayment(
	ctx context.Context,
	request string,
	amount uint64,
	maxFee uint64,
) (PaymentStatus, error) {
	feeLimit := &lnrpc.FeeLimit{Limit: &lnrpc.FeeLimit_Fixed{Fixed: int64(maxFee)}}
	sendPaymentRequest := lnrpc.SendRequest{
		PaymentRequest: request,
		FeeLimit:       feeLimit,
	}

	bolt11, err := decodepay.Decodepay(request)
	if err != nil {
		return PaymentStatus{}, err
	}
	// if this is an amountless invoice, pay the amount specified
	if bolt11.MSatoshi == 0 {
		sendPaymentRequest.Amt = int64(amount)
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

func (lnd *LndClient) PayPartialAmount(
	ctx context.Context,
	request string,
	amountMsat uint64,
	maxFee uint64,
) (PaymentStatus, error) {
	payReq, err := lnd.grpcClient.DecodePayReq(ctx, &lnrpc.PayReqString{PayReq: request})
	if err != nil {
		return PaymentStatus{PaymentStatus: Failed}, err
	}

	feeLimit := &lnrpc.FeeLimit{Limit: &lnrpc.FeeLimit_Fixed{Fixed: int64(maxFee)}}
	queryRoutesRequest := lnrpc.QueryRoutesRequest{
		PubKey:   payReq.Destination,
		AmtMsat:  int64(amountMsat),
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
	mppRecord := lnrpc.MPPRecord{PaymentAddr: payReq.PaymentAddr, TotalAmtMsat: payReq.NumMsat}
	route.Hops[len(route.Hops)-1].MppRecord = &mppRecord

	paymentHashBytes, err := hex.DecodeString(payReq.PaymentHash)
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

func (lnd *LndClient) SubscribeInvoice(ctx context.Context, paymentHash string) (InvoiceSubscriptionClient, error) {
	hash, err := hex.DecodeString(paymentHash)
	if err != nil {
		return nil, err
	}
	invoiceSubRequest := &invoicesrpc.SubscribeSingleInvoiceRequest{
		RHash: hash,
	}
	lndInvoiceClient, err := lnd.invoicesClient.SubscribeSingleInvoice(ctx, invoiceSubRequest)
	if err != nil {
		return nil, err
	}
	invoiceSub := &LndInvoiceSub{
		paymentHash:      paymentHash,
		invoiceSubClient: lndInvoiceClient,
	}
	return invoiceSub, nil
}

type LndInvoiceSub struct {
	paymentHash      string
	invoiceSubClient invoicesrpc.Invoices_SubscribeSingleInvoiceClient
}

func (lndSub *LndInvoiceSub) Recv() (Invoice, error) {
	invoiceRes, err := lndSub.invoiceSubClient.Recv()
	if err != nil {
		return Invoice{}, err
	}
	invoiceSettled := invoiceRes.State == lnrpc.Invoice_SETTLED
	invoice := Invoice{
		PaymentRequest: invoiceRes.PaymentRequest,
		PaymentHash:    lndSub.paymentHash,
		Preimage:       hex.EncodeToString(invoiceRes.RPreimage),
		Settled:        invoiceSettled,
		Amount:         uint64(invoiceRes.Value),
	}
	return invoice, nil
}
