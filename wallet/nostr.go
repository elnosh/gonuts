package wallet

import (
	"encoding/json"
	"log"

	"github.com/elnosh/gonuts/cashu/nuts/nut18"
	n "github.com/nbd-wtf/go-nostr"
)

// handleNostrEvent processes a single nostr event.
func (w *Wallet) handleNostrEvent(event n.Event) {
	var payload nut18.PaymentRequestPayload
	err := json.Unmarshal([]byte(event.Content), &payload)
	if err != nil {
		return
	}
	// Log detection of a PaymentRequestPayload
	log.Printf("\n Detected PaymentRequestPayload: id=%s mint=%s proofs=%d", payload.Id, payload.Mint, len(payload.Proofs))

	amount, err := w.ReceivePaymentRequestPayload(payload)
	if err != nil {
		log.Printf("error processing payment request payload: %v", err)
		return
	}
	log.Printf("\n Received  %+v Sats", amount)
}

func processNostrRequests(wallet *Wallet) {
	if wallet.nostrClient == nil || wallet.nostrClient.GetEventsChan() == nil {
		log.Println("\n Nostr client or event channel is not available, stopping request processing.")
		return
	}
	eventsChan := wallet.nostrClient.GetEventsChan()

	for {
		event, ok := <-eventsChan
		if !ok {
			log.Println("Nostr events channel closed, will retry acquiring channel")
			break
		}
		wallet.handleNostrEvent(event)
	}
}
