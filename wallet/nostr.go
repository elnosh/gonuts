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
		// not a payment request, ignore
		return
	}

	_, err = w.ReceivePaymentRequestPayload(payload)
	if err != nil {
		log.Printf("error processing payment request payload: %v", err)
	}
}

// processNostrRequests listens for events from the nostr client and exits when
// wallet.nostrCtx is cancelled. It assumes wallet.nostrClient has been set by
// the caller (LoadWallet) before starting this goroutine.
func processNostrRequests(wallet *Wallet) {
	if wallet == nil || wallet.nostrClient == nil {
		log.Println("processNostrRequests: nostr client is not available, exiting")
		return
	}

	eventsChan := wallet.nostrClient.GetEventsChan()
	log.Println("Listening for Nostr events")

	for {
		select {
		case <-wallet.nostrCtx.Done():
			log.Println("processNostrRequests: context cancelled, exiting listener")
			return
		case event, ok := <-eventsChan:
			if !ok {
				log.Println("Nostr events channel closed, exiting listener")
				return
			}
			wallet.handleNostrEvent(event)
		}
	}
}
