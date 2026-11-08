package main

import (
	"context"
	"fmt"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

func main() {
	ctx := context.Background()
	start, err := api.StartQrLogin(ctx)
	if err != nil {
		fmt.Println("start failed:", err)
		return
	}
	fmt.Println("content:", start.Content)
	fmt.Println("expires:", start.ExpiresIn)
	for i := 0; i < 3; i++ {
		res, err := api.PollQrLogin(ctx, start.Handle)
		if err != nil {
			fmt.Println(i, "poll err:", err)
		} else {
			fmt.Printf("%d status=%s msg=%s\n", i, res.Status, res.Message)
		}
		time.Sleep(2 * time.Second)
	}
}
