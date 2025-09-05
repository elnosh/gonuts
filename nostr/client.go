package nostr

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/elnosh/gonuts/cashu/nuts/nut18"
	n "github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/keyer"
	"github.com/nbd-wtf/go-nostr/nip17"
	"github.com/nbd-wtf/go-nostr/nip19"
)

type SeenPaymentPayloads struct {
	// Ids are for the nostr event id
	seenNostrIds map[string]bool
	sync.RWMutex
}

func (s *SeenPaymentPayloads) AddNostrId(id string) {
	s.Lock()
	defer s.Unlock()
	s.seenNostrIds[id] = true
}

func (s *SeenPaymentPayloads) PaymentIdAlreadyExists(id string) bool {
	s.RLock()
	defer s.RUnlock()
	_, exists := s.seenNostrIds[id]
	return exists
}

type NostrClient struct {
	relaysUrl          []string
	simplePool         *n.SimplePool
	keyer              n.Keyer
	SeenPaymentPayload *SeenPaymentPayloads
	chanEvent          chan n.Event
}

var (
	DefaultRelays = []string{"wss://relay.damus.io/", "wss://relay.primal.net/", "wss://relay.snort.social/", "wss://nos.lol"}
)

func SetupNostrClient(privateKey *secp256k1.PrivateKey, since_time_check n.Timestamp, relays []string) (*NostrClient, error) {
	if privateKey == nil {
		return nil, fmt.Errorf("passed private key is nil")
	}
	ctx := context.Background()

	selectedRelays := relays

	keyer, err := keyer.NewPlainKeySigner(hex.EncodeToString(privateKey.Serialize()))
	if err != nil {
		return nil, err
	}

	simplePool := n.NewSimplePool(ctx)
	if simplePool == nil {
		return nil, fmt.Errorf("Simple pool is nil")
	}

	client := NostrClient{
		relaysUrl: selectedRelays,
		keyer:     keyer,
		SeenPaymentPayload: &SeenPaymentPayloads{
			seenNostrIds: make(map[string]bool),
		},
		simplePool: simplePool,
	}

	eventWatch := nip17.ListenForMessages(ctx, client.simplePool, client.keyer, client.relaysUrl, since_time_check)

	client.chanEvent = eventWatch

	return &client, nil
}

// Close properly cleans up the NostrClient resources
// This should be called when the client is no longer needed to prevent memory leaks
func (nc *NostrClient) Close() error {
	if nc == nil {
		return nil
	}

	// Close the event channel if it exists
	if nc.chanEvent != nil {
		close(nc.chanEvent)
		nc.chanEvent = nil
	}

	// Close the simple pool to terminate all relay connections
	if nc.simplePool != nil {
		for _, url := range nc.relaysUrl {
			nc.simplePool.Close(url)
		}
		nc.simplePool = nil
	}

	// Clear sensitive keyer data for security
	nc.keyer = nil

	// Clear other fields
	nc.relaysUrl = nil

	return nil
}

// GetKeyer returns the keyer for the NostrClient
func (nc *NostrClient) GetKeyer() n.Keyer {
	return nc.keyer
}

// GetEventsChan returns the event channel for the NostrClient
func (nc *NostrClient) GetEventsChan() chan n.Event {
	return nc.chanEvent
}

// This send the payment payload as a nip17 dm
func (nc *NostrClient) SendPaymentToNostrProfile(payload nut18.PaymentRequestPayload, nprofile string) error {
	ctx := context.Background()
	_, value, err := nip19.Decode(nprofile)
	if err != nil {
		return err
	}
	profile, ok := value.(n.ProfilePointer)
	if !ok {
		return fmt.Errorf("could not parse nostr profile")
	}
	payloadJson, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return nip17.PublishMessage(ctx, string(payloadJson), n.Tags{}, nc.simplePool, nc.relaysUrl, profile.Relays, nc.keyer, profile.PublicKey, func(e *n.Event) {})
}
