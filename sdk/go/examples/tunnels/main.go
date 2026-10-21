// Calistirma: ZORVEN_TOKEN=zrv_api_... go run ./examples/tunnels
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	zorven "github.com/tkodcumpeg4/zorven/sdk/go"
)

func main() {
	token := os.Getenv("ZORVEN_TOKEN")
	if token == "" {
		log.Fatal("ZORVEN_TOKEN gerekli (panel > API Anahtarlari)")
	}
	c := zorven.NewClient(zorven.Options{
		Token:   token,
		BaseURL: os.Getenv("ZORVEN_API"),     // bos = https://zorven.app/api/v1
		Project: os.Getenv("ZORVEN_PROJECT"), // opsiyonel proje slug'i
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	for t, err := range c.Tunnels.Iter(ctx, &zorven.ListOptions{Limit: 50}) {
		if err != nil {
			if ae, ok := zorven.AsAPIError(err); ok {
				log.Fatalf("API hatasi: %d %s (%s)", ae.Status, ae.Code, ae.RequestID)
			}
			log.Fatal(err)
		}
		fmt.Printf("%s -> %s (enabled=%v)\n", t.ID, t.Target, t.Enabled)
		for _, h := range t.Hostnames {
			fmt.Println("   https://" + h.FQDN)
		}
	}
}
