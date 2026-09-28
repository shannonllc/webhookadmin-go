package webhookadmin_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	webhookadmin "github.com/shannonllc/webhookadmin-go"
)

func Example() {
	client, err := webhookadmin.NewClient("sk_live_...") // or set WEBHOOK_ADMIN_API_KEY
	if err != nil {
		log.Fatal(err)
	}
	res, err := client.Messages.Send(context.Background(), webhookadmin.SendMessageParams{
		Consumer:  "cus_123",
		EventType: "invoice.paid",
		Payload:   map[string]any{"invoice_id": "inv_1", "amount": 1200},
	}, webhookadmin.WithIdempotencyKey("invoice-inv_1-paid"))
	var e *webhookadmin.Error
	switch {
	case errors.As(err, &e) && e.Kind == webhookadmin.KindValidation:
		fmt.Println(e.Fields)
	case err != nil:
		log.Fatal(err)
	default:
		fmt.Println(res.ID, res.Deliveries)
	}
}

func ExampleWebhook_Verify() {
	wh, err := webhookadmin.NewWebhook(os.Getenv("WEBHOOK_SECRET")) // whsec_...
	if err != nil {
		log.Fatal(err)
	}
	http.HandleFunc("/webhooks", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		event, err := wh.Verify(body, r.Header)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fmt.Println(event.Type, string(event.Data))
		w.WriteHeader(http.StatusNoContent)
	})
}

func ExampleMessagesService_ListAll() {
	client, _ := webhookadmin.NewClient("sk_live_...")
	ctx := context.Background()
	it := client.Messages.ListAll(&webhookadmin.MessageListParams{Status: webhookadmin.MessageFailed})
	for it.Next(ctx) {
		fmt.Println(it.Current().ID)
	}
	if err := it.Err(); err != nil {
		log.Fatal(err)
	}
}
