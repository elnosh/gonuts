package lightning

// Client interface to interact with a Lightning backend
type Client interface {
	CreateInvoice(amount uint64) (Invoice, error)
	InvoiceStatus(hash string) (Invoice, error)
	FeeReserve(amount uint64) uint64
	SendPayment(request string, amount uint64) (string, error)
}

type Invoice struct {
	PaymentRequest string
	PaymentHash    string
	Settled        bool
	Amount         uint64
	Expiry         uint64
}
