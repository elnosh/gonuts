package pubsub

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

type Message struct {
	topic   string
	payload []byte
}

func NewMessage(msg []byte, topic string) *Message {
	return &Message{
		topic:   topic,
		payload: msg,
	}
}

func (m *Message) Topic() string {
	return m.topic
}

func (m *Message) Payload() []byte {
	return m.payload
}

type Subscribers map[string]*Subscriber

type PubSub struct {
	topics map[string]Subscribers
	mu     sync.RWMutex
}

func NewPubSub() *PubSub {
	return &PubSub{
		topics: make(map[string]Subscribers),
	}
}

func (b *PubSub) Subscribe(topic string) *Subscriber {
	b.mu.Lock()
	if b.topics[topic] == nil {
		b.topics[topic] = make(Subscribers)
	}
	s := NewSubscriber()
	b.topics[topic][s.id] = s
	b.mu.Unlock()

	return s
}

func (b *PubSub) Unsubscribe(s *Subscriber, topic string) {
	b.mu.Lock()
	delete(b.topics[topic], s.id)
	b.mu.Unlock()
}

func (b *PubSub) Publish(topic string, msg []byte) {
	b.mu.Lock()
	topicSubscribers := b.topics[topic]
	b.mu.Unlock()

	for _, s := range topicSubscribers {
		m := NewMessage(msg, topic)
		if !s.active {
			continue
		}

		go func(s *Subscriber) {
			s.signal(m)
		}(s)
	}
}

type Subscriber struct {
	id       string
	messages chan *Message
	active   bool
	mu       sync.RWMutex
}

func NewSubscriber() *Subscriber {
	id := make([]byte, 32)
	rand.Read(id)

	return &Subscriber{
		id:       hex.EncodeToString(id),
		messages: make(chan *Message),
		active:   true,
	}
}

func (s *Subscriber) signal(msg *Message) {
	s.mu.Lock()
	if s.active {
		s.messages <- msg
	}
	s.mu.Unlock()
}

func (s *Subscriber) GetMessages() <-chan *Message {
	return s.messages
}

func (s *Subscriber) Close() {
	s.mu.Lock()
	s.active = false
	s.mu.Unlock()
	close(s.messages)
}
