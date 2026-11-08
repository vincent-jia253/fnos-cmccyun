package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

func main() {
	ctx := context.Background()
	start, err := api.StartQrLogin(ctx)
	if err != nil {
		fmt.Println("start:", err)
		return
	}
	fmt.Println("CONTENT", start.Content)
	os.WriteFile("/tmp/qr-content.txt", []byte(start.Content), 0o644)
	for i := 0; i < 30; i++ {
		res, err := api.PollQrLogin(ctx, start.Handle)
		if err != nil {
			fmt.Println("poll err:", err)
			continue
		}
		fmt.Printf("%s poll#%02d status=%s code=%s msg=%s\n", time.Now().Format("15:04:05"), i+1, res.Status, res.Code, res.Message)
		if res.Status == api.QrConfirmed || res.Status == api.QrExpired || res.Status == api.QrCancelled {
			return
		}
		time.Sleep(3 * time.Second)
	}
}
