package main

import (
	"fmt"
	"github.com/docker/engine-api/client"
	"github.com/docker/engine-api/types"
	"github.com/docker/engine-api/types/container"
	"github.com/docker/engine-api/types/network"
	"golang.org/x/net/context"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"time"
)

func writeConn(conn net.Conn, data []byte) error {
	log.Printf("Want to write %d bytes", len(data))
	var start, c int
	var err error
	for {
		if c, err = conn.Write(data[start:]); err != nil {
			return err
		}
		start += c
		log.Printf("Wrote %d of %d bytes", start, len(data))
		if c == 0 || start == len(data) {
			break
		}
	}
	return nil
}

func main() {
	cli, err := client.NewEnvClient()
	if err != nil {
		panic(err)
	}

	options := types.ImageListOptions{All: true}
	images, err := cli.ImageList(context.Background(), options)
	if err != nil {
		panic(err)
	}

	for _, c := range images {
		fmt.Println(c.RepoTags)
	}

	config := &container.Config{
		Cmd:         []string{"sh", "-c", "g++ -std=c++11 main.cpp -o binary.exe && ./binary.exe"},
		Image:       "gcc",
		WorkingDir:  "/app",
		AttachStdin: true,
		OpenStdin:   true,
		StdinOnce:   true,
	}
	hostConfig := &container.HostConfig{
		Binds: []string{
			"/Users/madhavjha/src/github.com/maddyonline/moredocker:/app",
		},
	}
	resp, err := cli.ContainerCreate(context.Background(), config, hostConfig, &network.NetworkingConfig{}, "myapp")
	if err != nil {
		panic(err)
	}
	containerId := resp.ID

	err = cli.ContainerStart(context.Background(), containerId, types.ContainerStartOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println("OK")

	reader, err := cli.ContainerLogs(context.Background(), containerId, types.ContainerLogsOptions{
		ShowStdout: true,
		Follow:     true,
	})
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		_, err = io.Copy(os.Stdout, reader)
		if err != nil && err != io.EOF {
			log.Fatal(err)
		}
	}()

	time.Sleep(1 * time.Second)

	hijackedResp, err := cli.ContainerAttach(context.Background(), containerId, types.ContainerAttachOptions{
		Stdin:  true,
		Stream: true,
	})

	if err != nil {
		panic(err)
	}
	file, err := os.Open("input.txt")
	data, err := ioutil.ReadAll(file)
	defer file.Close()
	if err != nil {
		log.Printf("Got error: %v", err)
		data = []byte("bye\ncool\n")
	}
	go func(data []byte, conn net.Conn) {
		defer func() { fmt.Println("Done writing") }()
		defer conn.Close()
		err := writeConn(conn, data)
		if err != nil {
			log.Fatal(err)
		}
	}(data, hijackedResp.Conn)

	ch := make(chan int)
	<-ch
}