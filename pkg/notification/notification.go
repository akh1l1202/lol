package notification

import (
	"context"
	"fmt"
	"log"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"
)

// Message is the push notification payload
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Dispatcher is the messaging service interface
type Dispatcher interface {
	Send(token string, msg Message) error
}

// MockDispatcher logs push notifications to console/stdout for local testing
type MockDispatcher struct{}

func NewMockDispatcher() *MockDispatcher {
	return &MockDispatcher{}
}

func (d *MockDispatcher) Send(token string, msg Message) error {
	log.Printf("[FCM SIMULATION] Dispatched notification: Title=%q, Body=%q to Token=%q", msg.Title, msg.Body, token)
	return nil
}

// FirebaseDispatcher sends real push notifications via FCM Admin SDK
type FirebaseDispatcher struct {
	client *messaging.Client
}

func NewFirebaseDispatcher(credentialsPath string) (*FirebaseDispatcher, error) {
	ctx := context.Background()
	opt := option.WithCredentialsFile(credentialsPath)
	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		return nil, fmt.Errorf("error initializing firebase app: %w", err)
	}
	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting messaging client: %w", err)
	}
	return &FirebaseDispatcher{client: client}, nil
}

func (d *FirebaseDispatcher) Send(token string, msg Message) error {
	ctx := context.Background()
	_, err := d.client.Send(ctx, &messaging.Message{
		Token: token,
		Notification: &messaging.Notification{
			Title: msg.Title,
			Body:  msg.Body,
		},
	})
	if err != nil {
		return fmt.Errorf("fcm send error: %w", err)
	}
	log.Printf("[FCM] Sent message successfully: %q", msg.Title)
	return nil
}
