package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"

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

type App struct {
	client *client.Client

	networkIDs []string

	wg   sync.WaitGroup
	stop chan struct{}
}

func NewApp() *App {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	return &App{
		client: cli,

		stop: make(chan struct{}),
	}
}

func (a *App) Stop() {
	close(a.stop)
}

func (a *App) Wait() {
	a.wg.Wait()
}

func (a *App) PullImage(ctx context.Context) {
	r, err := a.client.ImagePull(ctx, alpineImage, types.ImagePullOptions{})
	if err != nil {
		panic(err)
	}
	defer r.Close()
	io.Copy(os.Stdout, r)
}

func (a *App) CreateNetworks(ctx context.Context) {
	f := filters.NewArgs()
	for k, v := range labels {
		f.Add("label", k+"="+v)
	}
	_, err := a.client.NetworksPrune(ctx, f)
	if err != nil {
		panic(err)
	}

	const nNetworks = 3
	a.networkIDs = make([]string, nNetworks)
	for i := 0; i < nNetworks; i++ {
		a.networkIDs[i] = a.createNetwork(ctx, i)
	}
}

func (a *App) createNetwork(ctx context.Context, i int) string {
	name := fmt.Sprintf("containerburn-%d", i)
	resp, err := a.client.NetworkCreate(ctx, name, types.NetworkCreate{
		Labels: labels,
	})
	if err != nil {
		panic(err)
	}

	return resp.ID
}

func (a *App) StartContainers(ctx context.Context) {
	nRunners := runtime.GOMAXPROCS(0) * 3

	// Unbuffered channel because we only send sequential ints,
	// and this ensures we don't quit with extra buffered work to do.
	idxCh := make(chan int)

	for i := 0; i < nRunners; i++ {
		a.wg.Add(1)
		go a.runContainer(ctx, idxCh)
	}

	a.wg.Add(1)
	go a.createWork(ctx, idxCh)
}

func (a *App) createWork(ctx context.Context, ch chan<- int) {
	defer a.wg.Done()
	defer close(ch)

	for idx := 0; ; idx++ {
		// Do one select without attempting to write to ch,
		// to detect a stop without contending for write.
		select {
		case <-ctx.Done():
			return
		case <-a.stop:
			return
		default:
			// Keep going.
		}

		select {
		case ch <- idx:
			// Keep going.
		case <-ctx.Done():
			return
		case <-a.stop:
			return
		}
	}
}

func (a *App) runContainer(ctx context.Context, idxCh <-chan int) {
	defer a.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stop:
			return
		case idx, ok := <-idxCh:
			if !ok {
				return
			}
			a.runSleep(ctx, idx)
		}
	}
}

// Fake exposed port.
// Nothing listens on this, but using a const for consistency.
const exposedPort = "8080/tcp"

func (a *App) runSleep(ctx context.Context, idx int) {
	name := fmt.Sprintf("burn-%d", idx)

	// Pick an arbitrary network for this container.
	networkID := a.networkIDs[idx%len(a.networkIDs)]

	dur := rand.Intn(10) + 1

	fmt.Printf("%s will sleep for %d seconds\n", name, dur)

	resp, err := a.client.ContainerCreate(
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

	if err := a.client.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		panic(err)
	}

	waitCh, errCh := a.client.ContainerWait(ctx, resp.ID, container.WaitConditionRemoved)

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
