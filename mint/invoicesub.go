package mint

import (
	"time"

	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/mint/lightning"
)

// checkInvoicePaid should be called in a different goroutine to check in the background
// if the invoice for the quoteId gets paid and update it in the db.
func (m *Mint) checkInvoicePaid(quoteId string) {
	mintQuote, err := m.db.GetMintQuote(quoteId)
	if err != nil {
		m.logErrorf("could not get mint quote '%v' from db: %v", quoteId, err)
		return
	}

	invoiceSub, err := m.lightningClient.SubscribeInvoice(mintQuote.PaymentHash)
	if err != nil {
		m.logErrorf("could not subscribe to invoice changes for mint quote '%v': %v", quoteId, err)
		return
	}

	updateChan := make(chan lightning.Invoice)
	errChan := make(chan error)

	go func() {
		for {
			invoice, err := invoiceSub.Recv()
			if err != nil {
				errChan <- err
				return
			}

			// only send on channel if invoice gets settled
			if invoice.Settled {
				updateChan <- invoice
				return
			}
		}

	}()

	timeUntilExpiry := int64(mintQuote.Expiry) - time.Now().Unix()

	select {
	case invoice := <-updateChan:
		if invoice.Settled {
			m.logInfof("received update from invoice sub. Invoice for mint quote '%v' is PAID", mintQuote.Id)
			if err := m.db.UpdateMintQuoteState(mintQuote.Id, nut04.Paid); err != nil {
				m.logErrorf("could not mark mint quote '%v' as PAID in db: %v", mintQuote.Id, err)
			}
		}
	case err := <-errChan:
		m.logErrorf("error reading from invoice subscription: %v", err)
	case <-time.After(time.Second * time.Duration(timeUntilExpiry)):
		// cancel when quote reaches expiry time
		m.logDebugf("canceling invoice subscription for quote '%v'. Reached deadline", mintQuote.Id)
	}
}
