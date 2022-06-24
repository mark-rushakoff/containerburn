package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"time"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	a := NewApp()

	ctx := context.Background()

	a.PullImage(ctx)
	a.CreateNetworks(ctx)
	a.StartContainers(ctx)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	<-sig

	a.Stop()
	fmt.Println("Waiting for containers to stop...")
	a.Wait()
}
