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
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

const alpineImage = "docker.io/alpine:latest"

var labels = map[string]string{"containerburn": "true"}

func main() {
	rand.Seed(time.Now().UnixNano())

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	pullImage(ctx, cli)

	networkIDs := createNetworks(ctx, cli)

	nRunners := runtime.GOMAXPROCS(0) * 3
	idxCh := make(chan int, nRunners)

	for i := 0; i < nRunners; i++ {
		go runContainer(ctx, cli, networkIDs, idxCh)
	}

	for idx := 0; ; idx++ {
		idxCh <- idx
	}
}

func pullImage(ctx context.Context, cli *client.Client) {
	r, err := cli.ImagePull(ctx, alpineImage, types.ImagePullOptions{})
	if err != nil {
		panic(err)
	}
	defer r.Close()
	io.Copy(os.Stdout, r)
}

func createNetworks(ctx context.Context, cli *client.Client) []string {
	f := filters.NewArgs()
	for k, v := range labels {
		f.Add("label", k+"="+v)
	}
	_, err := cli.NetworksPrune(ctx, f)
	if err != nil {
		panic(err)
	}

	const nNetworks = 3
	networkIDs := make([]string, nNetworks)
	for i := 0; i < nNetworks; i++ {
		networkIDs[i] = createNetwork(ctx, cli, i)
	}
	return networkIDs
}

func createNetwork(ctx context.Context, cli *client.Client, i int) string {
	name := fmt.Sprintf("containerburn-%d", i)
	resp, err := cli.NetworkCreate(ctx, name, types.NetworkCreate{
		Labels: map[string]string{
			"containerburn": "true",
		},
	})
	if err != nil {
		panic(err)
	}

	return resp.ID
}

func runContainer(ctx context.Context, cli *client.Client, networkIDs []string, idxCh <-chan int) {
	for idx := range idxCh {
		// Pick an arbitrary network for this container.
		networkID := networkIDs[idx%len(networkIDs)]
		runSleep(ctx, cli, networkID, idx)
	}
}

// Fake exposed port.
// Nothing listens on this, but using a const for consistency.
const exposedPort = "8080/tcp"

func runSleep(ctx context.Context, cli *client.Client, networkID string, idx int) {
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
			EndpointsConfig: map[string]*network.EndpointSettings{
				networkID: {},
			},
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
