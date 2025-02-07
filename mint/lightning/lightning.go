package lightning

import "context"

// Client interface to interact with a Lightning backend
type Client interface {
	ConnectionStatus() error
	CreateInvoice(amount uint64) (Invoice, error)
	InvoiceStatus(hash string) (Invoice, error)
	SendPayment(ctx context.Context, request string, maxFee uint64) (PaymentStatus, error)
	PayPartialAmount(ctx context.Context, request string, amountMsat uint64, maxFee uint64) (PaymentStatus, error)
	OutgoingPaymentStatus(ctx context.Context, hash string) (PaymentStatus, error)
	FeeReserve(amount uint64) uint64
	SubscribeInvoice(ctx context.Context, paymentHash string) (InvoiceSubscriptionClient, error)
}

type Invoice struct {
	PaymentRequest string
	PaymentHash    string
	Preimage       string
	Settled        bool
	Amount         uint64
	Expiry         uint64
}

type State int

const (
	Succeeded State = iota
	Failed
	Pending
)

type PaymentStatus struct {
	Preimage             string
	PaymentStatus        State
	PaymentFailureReason string
}

// InvoiceSubscriptionClient subscribes to get updates on the status of an invoice
type InvoiceSubscriptionClient interface {
	// This blocks until there is an update
	Recv() (Invoice, error)
}
