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
	FakePreimage           = "0000000000000000"
	FailPaymentDescription = "fail the payment"
)

type FakeBackendInvoice struct {
	PaymentRequest string
	PaymentHash    string
	Preimage       string
	Status         State
	Amount         uint64
}

func (i *FakeBackendInvoice) ToInvoice() Invoice {
	return Invoice{
		PaymentRequest: i.PaymentRequest,
		PaymentHash:    i.PaymentHash,
		Preimage:       i.Preimage,
		Settled:        i.Status == Succeeded,
		Amount:         i.Amount,
	}
}

type FakeBackend struct {
	Invoices     []FakeBackendInvoice
	PaymentDelay int64
}

func (fb *FakeBackend) ConnectionStatus() error { return nil }

func (fb *FakeBackend) CreateInvoice(amount uint64) (Invoice, error) {
	req, preimage, paymentHash, err := CreateFakeInvoice(amount, false)
	if err != nil {
		return Invoice{}, err
	}

	fakeInvoice := FakeBackendInvoice{
		PaymentRequest: req,
		PaymentHash:    paymentHash,
		Preimage:       preimage,
		Status:         Succeeded,
		Amount:         amount,
	}
	fb.Invoices = append(fb.Invoices, fakeInvoice)

	return fakeInvoice.ToInvoice(), nil
}

func (fb *FakeBackend) InvoiceStatus(hash string) (Invoice, error) {
	invoiceIdx := slices.IndexFunc(fb.Invoices, func(i FakeBackendInvoice) bool {
		return i.PaymentHash == hash
	})
	if invoiceIdx == -1 {
		return Invoice{}, errors.New("invoice does not exist")
	}

	return fb.Invoices[invoiceIdx].ToInvoice(), nil
}

func (fb *FakeBackend) SendPayment(ctx context.Context, request string, maxFee uint64) (PaymentStatus, error) {
	invoice, err := decodepay.Decodepay(request)
	if err != nil {
		return PaymentStatus{}, fmt.Errorf("error decoding invoice: %v", err)
	}

	status := Succeeded
	if invoice.Description == FailPaymentDescription {
		status = Failed
	} else if fb.PaymentDelay > 0 {
		if time.Now().Unix() < int64(invoice.CreatedAt)+fb.PaymentDelay {
			status = Pending
		}
	}

	outgoingPayment := FakeBackendInvoice{
		PaymentHash: invoice.PaymentHash,
		Preimage:    FakePreimage,
		Status:      status,
		Amount:      uint64(invoice.MSatoshi) * 1000,
	}
	fb.Invoices = append(fb.Invoices, outgoingPayment)

	return PaymentStatus{
		Preimage:      FakePreimage,
		PaymentStatus: status,
	}, nil
}

func (fb *FakeBackend) PayPartialAmount(ctx context.Context, request string, amountMsat, maxFee uint64) (PaymentStatus, error) {
	invoice, err := decodepay.Decodepay(request)
	if err != nil {
		return PaymentStatus{}, fmt.Errorf("error decoding invoice: %v", err)
	}

	status := Succeeded
	if invoice.Description == FailPaymentDescription {
		status = Failed
	} else if fb.PaymentDelay > 0 {
		if time.Now().Unix() < int64(invoice.CreatedAt)+fb.PaymentDelay {
			status = Pending
		}
	}

	outgoingPayment := FakeBackendInvoice{
		PaymentHash: invoice.PaymentHash,
		Preimage:    FakePreimage,
		Status:      status,
		Amount:      uint64(invoice.MSatoshi) * 1000,
	}
	fb.Invoices = append(fb.Invoices, outgoingPayment)

	return PaymentStatus{
		Preimage:      FakePreimage,
		PaymentStatus: status,
	}, nil
}

func (fb *FakeBackend) OutgoingPaymentStatus(ctx context.Context, hash string) (PaymentStatus, error) {
	invoiceIdx := slices.IndexFunc(fb.Invoices, func(i FakeBackendInvoice) bool {
		return i.PaymentHash == hash
	})
	if invoiceIdx == -1 {
		return PaymentStatus{}, errors.New("payment does not exist")
	}

	return PaymentStatus{
		Preimage:      FakePreimage,
		PaymentStatus: fb.Invoices[invoiceIdx].Status,
	}, nil
}

func (fb *FakeBackend) FeeReserve(amount uint64) uint64 {
	return 0
}

func (fb *FakeBackend) SubscribeInvoice(paymentHash string) (InvoiceSubscriptionClient, error) {
	return &FakeInvoiceSub{
		paymentHash: paymentHash,
		fb:          fb,
	}, nil
}

type FakeInvoiceSub struct {
	paymentHash string
	fb          *FakeBackend
}

func (fakeSub *FakeInvoiceSub) Recv() (Invoice, error) {
	invoiceIdx := slices.IndexFunc(fakeSub.fb.Invoices, func(i FakeBackendInvoice) bool {
		return i.PaymentHash == fakeSub.paymentHash
	})
	if invoiceIdx == -1 {
		return Invoice{}, errors.New("invoice does not exist")
	}

	return fakeSub.fb.Invoices[invoiceIdx].ToInvoice(), nil
}

func (fb *FakeBackend) SetInvoiceStatus(hash string, status State) {
	invoiceIdx := slices.IndexFunc(fb.Invoices, func(i FakeBackendInvoice) bool {
		return i.PaymentHash == hash
	})
	if invoiceIdx == -1 {
		return
	}
	fb.Invoices[invoiceIdx].Status = status
}

func CreateFakeInvoice(amount uint64, failPayment bool) (string, string, string, error) {
	var random [32]byte
	_, err := rand.Read(random[:])
	if err != nil {
		return "", "", "", err
	}
	preimage := hex.EncodeToString(random[:])
	paymentHash := sha256.Sum256(random[:])
	hash := hex.EncodeToString(paymentHash[:])

	description := "test"
	if failPayment {
		description = FailPaymentDescription
	}

	invoice, err := zpay32.NewInvoice(
		&chaincfg.SigNetParams,
		paymentHash,
		time.Now(),
		zpay32.Amount(lnwire.MilliSatoshi(amount*1000)),
		zpay32.Description(description),
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
