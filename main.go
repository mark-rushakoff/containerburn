package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

const alpineImage = "docker.io/alpine:latest"

func main() {
	rand.Seed(time.Now().UnixNano())

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	r, err := cli.ImagePull(ctx, alpineImage, types.ImagePullOptions{})
	if err != nil {
		panic(err)
	}
	defer r.Close()
	io.Copy(os.Stdout, r)
	r.Close()

	nRunners := runtime.GOMAXPROCS(0) * 3
	idxCh := make(chan int, nRunners)

	for i := 0; i < nRunners; i++ {
		go runContainer(ctx, cli, idxCh)
	}

	for idx := 0; ; idx++ {
		idxCh <- idx
	}
}

func runContainer(ctx context.Context, cli *client.Client, idxCh <-chan int) {
	for idx := range idxCh {
		runSleep(ctx, cli, idx)
	}
}

// Fake exposed port.
// Nothing listens on this, but using a const for consistency.
const exposedPort = "8080/tcp"

func runSleep(ctx context.Context, cli *client.Client, idx int) {
	name := fmt.Sprintf("burn-%d", idx)

	dur := rand.Intn(10) + 1

	fmt.Printf("%s will sleep for %d seconds\n", name, dur)

	resp, err := cli.ContainerCreate(
		ctx,
		&container.Config{
			Hostname: name,
			Image:    alpineImage,
			Cmd:      []string{"sleep", strconv.Itoa(dur)},

			ExposedPorts: nat.PortSet{
				exposedPort: struct{}{},
			},
		},
		&container.HostConfig{
			AutoRemove: true,
			PortBindings: nat.PortMap{
				exposedPort: []nat.PortBinding{
					{HostIP: "127.0.0.1"},
				},
			},
		},
		&network.NetworkingConfig{
			EndpointsConfig: nil,
		},
		&specs.Platform{},
		name,
	)
	if err != nil {
		panic(err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		panic(err)
	}

	waitCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionRemoved)

	select {
	case <-ctx.Done():
		return
	case res := <-waitCh:
		if res.Error != nil {
			fmt.Printf("%s: wait: received error: %v\n", name, res.Error)
		}
		fmt.Printf("%s: wait: code=%d\n", name, res.StatusCode)
	case err := <-errCh:
		fmt.Printf("%s: error while waiting: %v\n", name, err)
	}
}
