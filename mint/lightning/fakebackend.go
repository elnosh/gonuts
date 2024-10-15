package lightning

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/zpay32"
	decodepay "github.com/nbd-wtf/ln-decodepay"
)

const (
	FakePreimage = "0000000000000000"
)

type FakeBackend struct {
	invoices []Invoice
}

func (fb *FakeBackend) ConnectionStatus() error { return nil }

func (fb *FakeBackend) CreateInvoice(amount uint64) (Invoice, error) {
	req, preimage, paymentHash, err := createFakeInvoice(amount)
	if err != nil {
		return Invoice{}, err
	}

	invoice := Invoice{
		PaymentRequest: req,
		PaymentHash:    paymentHash,
		Preimage:       preimage,
		Settled:        true,
		Amount:         amount,
		Expiry:         uint64(time.Now().Unix()),
	}
	fb.invoices = append(fb.invoices, invoice)

	return invoice, nil
}

func (fb *FakeBackend) InvoiceStatus(hash string) (Invoice, error) {
	invoiceIdx := slices.IndexFunc(fb.invoices, func(i Invoice) bool {
		return i.PaymentHash == hash
	})
	if invoiceIdx == -1 {
		return Invoice{}, errors.New("invoice does not exist")
	}

	return fb.invoices[invoiceIdx], nil
}

func (fb *FakeBackend) SendPayment(ctx context.Context, request string, amount uint64) (PaymentStatus, error) {
	invoice, err := decodepay.Decodepay(request)
	if err != nil {
		return PaymentStatus{}, fmt.Errorf("error decoding invoice: %v", err)
	}

	outgoingPayment := Invoice{
		PaymentRequest: request,
		PaymentHash:    invoice.PaymentHash,
		Preimage:       FakePreimage,
		Settled:        true,
	}
	fb.invoices = append(fb.invoices, outgoingPayment)

	return PaymentStatus{
		Preimage:      FakePreimage,
		PaymentStatus: Succeeded,
	}, nil
}

func (fb *FakeBackend) OutgoingPaymentStatus(ctx context.Context, hash string) (PaymentStatus, error) {
	invoiceIdx := slices.IndexFunc(fb.invoices, func(i Invoice) bool {
		return i.PaymentHash == hash
	})
	if invoiceIdx == -1 {
		return PaymentStatus{}, errors.New("payment does not exist")
	}

	return PaymentStatus{
		Preimage:      fb.invoices[invoiceIdx].Preimage,
		PaymentStatus: Succeeded,
	}, nil
}

func (fb *FakeBackend) FeeReserve(amount uint64) uint64 {
	return 0
}

func createFakeInvoice(amount uint64) (string, string, string, error) {
	var random [32]byte
	_, err := rand.Read(random[:])
	if err != nil {
		return "", "", "", err
	}
	preimage := hex.EncodeToString(random[:])
	paymentHash := sha256.Sum256(random[:])
	hash := hex.EncodeToString(paymentHash[:])

	invoice, err := zpay32.NewInvoice(
		&chaincfg.SigNetParams,
		paymentHash,
		time.Now(),
		zpay32.Amount(lnwire.MilliSatoshi(amount*1000)),
		zpay32.Description("test"),
	)
	if err != nil {
		return "", "", "", err
	}

	invoiceStr, err := invoice.Encode(zpay32.MessageSigner{
		SignCompact: func(msg []byte) ([]byte, error) {
			key, err := secp256k1.GeneratePrivateKey()
			if err != nil {
				return []byte{}, err
			}
			return ecdsa.SignCompact(key, msg, true), nil
		},
	})
	if err != nil {
		return "", "", "", err
	}

	return invoiceStr, preimage, hash, nil
}
