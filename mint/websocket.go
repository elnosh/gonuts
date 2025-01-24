package mint

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/elnosh/gonuts/cashu/nuts/nut04"
	"github.com/elnosh/gonuts/cashu/nuts/nut17"
	"github.com/elnosh/gonuts/mint/pubsub"
	"github.com/elnosh/gonuts/mint/storage"
	"github.com/gorilla/websocket"
)

const (
	BOLT11_MINT_QUOTE_TOPIC = "bolt11_mint_quote_topic"
	BOLT11_MELT_QUOTE_TOPIC = "bolt11_melt_quote_topic"
	PROOF_STATE_TOPIC       = "proof_state_topic"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type WebsocketManager struct {
	clients map[*Client]bool
	sync.RWMutex
	mint *Mint
}

func NewWebSocketManager(mint *Mint) *WebsocketManager {
	return &WebsocketManager{
		clients: make(map[*Client]bool),
		mint:    mint,
	}
}

func (wm *WebsocketManager) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		wm.mint.logErrorf("could not upgrade to websocket connection: %v", err)
		return
	}

	client := NewClient(conn, wm)
	wm.addClient(client)

	wm.mint.logInfof("websocket connection established.")

	go client.readMessages()
	go client.writeMessages()
}

func (wm *WebsocketManager) addClient(client *Client) {
	wm.Lock()
	wm.clients[client] = true
	wm.Unlock()
}

func (wm *WebsocketManager) removeClient(client *Client) {
	wm.Lock()
	if _, ok := wm.clients[client]; ok {
		client.close()
		delete(wm.clients, client)
	}
	wm.Unlock()
}

type Client struct {
	conn          *websocket.Conn
	subscriptions map[string]SubscriptionClient
	mu            sync.Mutex
	manager       *WebsocketManager

	// aggregate writes through this channel since there can only be one concurrent writer.
	send chan json.RawMessage

	msgSizeLimit int64
	pongWait     time.Duration
	pingInterval time.Duration
}

func NewClient(conn *websocket.Conn, manager *WebsocketManager) *Client {
	return &Client{
		conn:          conn,
		subscriptions: make(map[string]SubscriptionClient),
		manager:       manager,
		send:          make(chan json.RawMessage),
		msgSizeLimit:  2048,
		pongWait:      60 * time.Second,
		pingInterval:  30 * time.Second,
	}
}

func (c *Client) readMessages() {
	defer c.manager.removeClient(c)

	if err := c.conn.SetReadDeadline(time.Now().Add(c.pongWait)); err != nil {
		return
	}

	c.conn.SetReadLimit(c.msgSizeLimit)
	c.conn.SetPongHandler(func(string) error {
		// increase deadline for next read to current time + pongWait
		// whenever it receives a pong response from a ping we sent
		return c.conn.SetReadDeadline(time.Now().Add(c.pongWait))
	})

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(
				err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived,
				websocket.CloseAbnormalClosure,
			) {
				c.manager.mint.logDebugf("detected unexpected closed connection: %v", err)
			}
			return
		}

		// this is the only type of message clients will send to the mint
		var wsRequest nut17.WsRequest
		if err := json.Unmarshal(msg, &wsRequest); err != nil {
			wsErr := nut17.NewWsError(1000, "invalid request", -1)
			c.manager.mint.logErrorf("Got invalid websocket request. Sending error message: %v", wsErr)
			jsonErrMsg, _ := json.Marshal(wsErr)
			c.send <- jsonErrMsg
		}

		wsResponse, wsError := c.processRequest(wsRequest)
		if wsError != nil {
			jsonErrMsg, _ := json.Marshal(wsError)
			c.manager.mint.logErrorf("Error processing websocket request. Sending error message: %v", wsError)
			c.send <- jsonErrMsg
		}

		// if successful request, send WsNotification with initial state
		jsonNotification, _ := json.Marshal(wsResponse)
		c.send <- jsonNotification
	}
}

func (c *Client) writeMessages() {
	ticker := time.NewTicker(c.pingInterval)
	defer func() {
		c.manager.mint.logInfof("removing websocket client. closing connection")
		c.manager.removeClient(c)
		defer ticker.Stop()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			// return if channel got closed. This means connection will be closed
			if !ok {
				return
			}

			c.manager.mint.logDebugf("sending websocket message: %s", msg)
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				c.manager.mint.logErrorf("could not write message on websocket connection: %v\n", err)
			}
		case <-ticker.C:
			if err := c.conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				c.manager.mint.logErrorf("could not write ping message: %v. closing websocket connection", err)
				return
			}
		}
	}
}

func (c *Client) processRequest(req nut17.WsRequest) (*nut17.WsResponse, *nut17.WsError) {
	// limit to 100 subs per connection
	if len(c.subscriptions) >= 100 {
		wsErr := nut17.NewWsError(1000, "reached subscription limit", req.Id)
		return nil, &wsErr
	}

	switch req.Method {
	case nut17.SUBSCRIBE:
		wsResponse, wsErr := c.subscriptionRequest(req)
		if wsErr != nil {
			return nil, wsErr
		}
		return wsResponse, nil

	case nut17.UNSUBSCRIBE:
		wsResponse, wsErr := c.unsubscriptionRequest(req)
		if wsErr != nil {
			return nil, wsErr
		}
		return wsResponse, nil
	}

	wsErr := nut17.NewWsError(1000, "invalid request method", req.Id)
	return nil, &wsErr
}

func (c *Client) subscriptionRequest(req nut17.WsRequest) (*nut17.WsResponse, *nut17.WsError) {
	if _, ok := c.subscriptions[req.Params.SubId]; ok {
		errMsg := fmt.Sprintf("subscription with subId '%v' already exists", req.Params.SubId)
		wsErr := nut17.NewWsError(1000, errMsg, req.Id)
		return nil, &wsErr
	}

	switch nut17.StringToKind(req.Params.Kind) {
	case nut17.Bolt11MintQuote:
		quoteIds := req.Params.Filters
		if len(quoteIds) > 50 {
			wsErr := nut17.NewWsError(1000, "too many filters", req.Id)
			return nil, &wsErr
		}

		quotes := make([]storage.MintQuote, len(quoteIds))
		for i, quoteId := range quoteIds {
			quote, err := c.manager.mint.db.GetMintQuote(quoteId)
			if err != nil {
				wsErr := nut17.NewWsError(1000, fmt.Sprintf("quote %v does not exist", quoteId), req.Id)
				return nil, &wsErr
			}
			quotes[i] = quote
		}

		mintQuotesClient := NewMintQuotesSubClient(req.Params.SubId, quotes, c.manager.mint.publisher)
		c.manager.mint.logDebugf("adding new subscription of kind '%s' with sub id '%v'", req.Params.Kind, req.Params.SubId)
		c.addSubscriptionClient(req.Params.SubId, mintQuotesClient)

		go func() {
			// send initial quote state
			for _, quote := range quotes {
				firstQuoteState := nut04.PostMintQuoteBolt11Response{
					Quote:   quote.Id,
					Request: quote.PaymentRequest,
					State:   quote.State,
					Expiry:  quote.Expiry,
				}
				jsonPayload, _ := json.Marshal(&firstQuoteState)
				wsNotif := nut17.WsNotification{
					JsonRPC: nut17.JSONRPC_2,
					Method:  nut17.SUBSCRIBE,
					Params: nut17.NotificationParams{
						SubId:   req.Params.SubId,
						Payload: jsonPayload,
					},
				}
				jsonNotification, _ := json.Marshal(wsNotif)
				c.send <- jsonNotification
			}
		}()

		go listenForSubscriptionUpdates(mintQuotesClient, c.send)

	case nut17.Bolt11MeltQuote:
	case nut17.ProofState:
	default:
		wsErr := nut17.NewWsError(1000, "invalid request method", req.Id)
		return nil, &wsErr
	}

	return &nut17.WsResponse{
		JsonRPC: nut17.JSONRPC_2,
		Result: nut17.Result{
			Status: nut17.OK,
			SubId:  req.Params.SubId,
		},
		Id: req.Id,
	}, nil
}

func (c *Client) unsubscriptionRequest(req nut17.WsRequest) (*nut17.WsResponse, *nut17.WsError) {
	_, ok := c.subscriptions[req.Params.SubId]
	if !ok {
		errMsg := fmt.Sprintf("subscription with subId '%v' does not exist", req.Params.SubId)
		wsErr := nut17.NewWsError(1000, errMsg, req.Id)
		return nil, &wsErr
	}

	c.manager.mint.logDebugf("got unsubscription request. Removing sub '%v'", req.Params.SubId)
	c.removeSubscriptionClient(req.Params.SubId)
	return &nut17.WsResponse{
		JsonRPC: nut17.JSONRPC_2,
		Result: nut17.Result{
			Status: nut17.OK,
			SubId:  req.Params.SubId,
		},
		Id: req.Id,
	}, nil
}

func (c *Client) addSubscriptionClient(subId string, subClient SubscriptionClient) {
	c.mu.Lock()
	c.subscriptions[subId] = subClient
	c.mu.Unlock()
}

func (c *Client) removeSubscriptionClient(subId string) {
	c.mu.Lock()
	if subClient, ok := c.subscriptions[subId]; ok {
		subClient.Close()
		delete(c.subscriptions, subId)
	}
	c.mu.Unlock()
}

// cancel all subscriptions and close websocket connection
func (c *Client) close() error {
	for _, subClient := range c.subscriptions {
		subClient.Close()
	}
	c.conn.Close()
	close(c.send)
	return nil
}

func listenForSubscriptionUpdates(subClient SubscriptionClient, send chan json.RawMessage) {
	notifChan := subClient.Read()
	for {
		select {
		case notif := <-notifChan:
			jsonNotification, _ := json.Marshal(notif)
			send <- jsonNotification
		case <-subClient.Context().Done():
			return
		}
	}
}

type SubscriptionClient interface {
	Read() <-chan nut17.WsNotification
	Context() context.Context
	Close()
}

type MintQuotesSubClient struct {
	subId  string
	ctx    context.Context
	cancel context.CancelFunc

	pubsub     *pubsub.PubSub
	subscriber *pubsub.Subscriber
	quotes     map[string]nut04.State
}

func NewMintQuotesSubClient(subId string, mintQuotes []storage.MintQuote, pubsub *pubsub.PubSub) *MintQuotesSubClient {
	ctx, cancel := context.WithCancel(context.Background())
	subscriber := pubsub.Subscribe(BOLT11_MINT_QUOTE_TOPIC)

	quotes := make(map[string]nut04.State)
	for _, quote := range mintQuotes {
		quotes[quote.Id] = quote.State
	}

	return &MintQuotesSubClient{
		pubsub:     pubsub,
		subId:      subId,
		ctx:        ctx,
		cancel:     cancel,
		quotes:     quotes,
		subscriber: subscriber,
	}
}

func (quotesClient *MintQuotesSubClient) Read() <-chan nut17.WsNotification {
	notifChan := make(chan nut17.WsNotification)

	// channel on which to receive db udpate events
	messagesChan := quotesClient.subscriber.GetMessages()

	go func() {
		for {
			select {
			case msg, ok := <-messagesChan:
				if !ok {
					return
				}

				var mintQuote storage.MintQuote
				json.Unmarshal(msg.Payload(), &mintQuote)

				previousState, ok := quotesClient.quotes[mintQuote.Id]
				if ok {
					// send notification if there was a state change
					if previousState != mintQuote.State {
						quotesClient.quotes[mintQuote.Id] = mintQuote.State

						newQuoteState := nut04.PostMintQuoteBolt11Response{
							Quote:   mintQuote.Id,
							Request: mintQuote.PaymentRequest,
							State:   mintQuote.State,
							Expiry:  mintQuote.Expiry,
						}
						notificationPayload, _ := json.Marshal(&newQuoteState)

						wsNotif := nut17.WsNotification{
							JsonRPC: nut17.JSONRPC_2,
							Method:  nut17.SUBSCRIBE,
							Params: nut17.NotificationParams{
								SubId:   quotesClient.subId,
								Payload: notificationPayload,
							},
						}
						notifChan <- wsNotif
					}
				}

			case <-quotesClient.ctx.Done():
				return
			}
		}
	}()

	return notifChan
}

func (quotesClient *MintQuotesSubClient) Context() context.Context {
	return quotesClient.ctx
}

func (quotesClient *MintQuotesSubClient) Close() {
	quotesClient.pubsub.Unsubscribe(quotesClient.subscriber, BOLT11_MINT_QUOTE_TOPIC)
	quotesClient.subscriber.Close()
	quotesClient.cancel()
}
