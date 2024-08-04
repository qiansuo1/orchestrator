package task

import (
	"context"
	"io"
	"log"
	"math"
	"os"

	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"

	"github.com/docker/docker/api/types"
)
type State int


type Task struct{
	Id uuid.UUID
	Name string
	ContainerID   string
	State State

	
	Cpu   float64
	Image string
	Memory int64
	Disk int64
	ExposedPorts nat.PortSet
	PortBindings map[string]string
	RestartPolicy string

	StartTime time.Time
	FinishTime time.Time

	HostPorts nat.PortMap

	HealthCheck   string
    RestartCount  int
} 

type TaskEvent struct{
	Id uuid.UUID
	State State
	Timestamp time.Time
	Task Task
}

type Config struct{
	Name string
	AttachStdin bool
	AttachStdout bool
	AttachStderr bool
	ExposedPorts nat.PortSet
	Cmd []string
	Image string
	
	Cpu float64
	Memory int64
	Disk int64
	Env []string
	RestartPolicy string
}

type Docker struct{
	Client *client.Client
	Config Config
	
}

type DockerResult struct {
	Error       error
	Action      string
	ContainerId string
	Result      string
}

type DockerInspectResponse struct{
	Error error
	Container *types.ContainerJSON
}
func NewConfig(t *Task) *Config {
	return &Config{
		Name:          t.Name,
		ExposedPorts:  t.ExposedPorts,
		Image:         t.Image,
		Cpu:           t.Cpu,
		Memory:        t.Memory,
		Disk:          t.Disk,
		RestartPolicy: t.RestartPolicy,
	}
}

func NewDocker(c *Config) *Docker {
	dc, _ := client.NewClientWithOpts(client.FromEnv,client.WithVersion("1.43"))
	return &Docker{
		Client: dc,
		Config: *c,
	}
}

func (d *Docker)Run()DockerResult{
	ctx := context.Background()
	reader,err:= d.Client.ImagePull(ctx,d.Config.Image,image.PullOptions{})
	if err != nil{
		log.Printf("Error pulling image %s: %v\n", d.Config.Image, err)
		return DockerResult{Error: err}
	}
	io.Copy(os.Stdout, reader)
	rp := container.RestartPolicy{
		Name: container.RestartPolicyMode(d.Config.RestartPolicy),
	}
	r := container.Resources{
		Memory: d.Config.Memory,
		NanoCPUs: int64(d.Config.Cpu * math.Pow(10, 9)),
	}
	
	cc := container.Config{
		Image: d.Config.Image,
		Tty: false,
		Env: d.Config.Env,
		ExposedPorts: d.Config.ExposedPorts,
	}
	hc := container.HostConfig{
		RestartPolicy: rp,
		Resources: r,
		PublishAllPorts: true,
	}
	resp,err := d.Client.ContainerCreate(ctx,&cc,&hc,nil,nil,d.Config.Name)
	if err != nil {
		log.Printf("Error creating container using image %s: %v\n", d.Config.Image, err)
		return DockerResult{Error: err}
	}
	err = d.Client.ContainerStart(ctx,resp.ID,container.StartOptions{})
	if err != nil{
		log.Printf("Error starting container %s: %v\n", resp.ID, err)
		return DockerResult{Error: err}
	}
	out,err := d.Client.ContainerLogs(ctx,resp.ID,container.LogsOptions{ShowStdout: true,ShowStderr: true})
	if err != nil {
		log.Printf("Error getting logs for container %s: %v\n", resp.ID, err)
		return DockerResult{Error: err}
	}
	stdcopy.StdCopy(os.Stdout,os.Stderr,out)
	return DockerResult{ContainerId: resp.ID, Action: "start", Result: "success"}
}

func (d *Docker)Stop(id string) DockerResult{
	log.Printf("Attempting to stop container %v", id)
	ctx := context.Background()
	err := d.Client.ContainerStop(ctx,id,container.StopOptions{})
	if err != nil {
		log.Printf("Error stopping container %s: %v\n", id, err)
		return DockerResult{Error: err}
	}
	return DockerResult{Action: "stop", Result: "success", Error: nil}
}

func (d *Docker) Inspect(containerID string) DockerInspectResponse {
    dc, _ := client.NewClientWithOpts(client.FromEnv)
    ctx := context.Background()
    resp, err := dc.ContainerInspect(ctx, containerID)
    if err != nil {
        log.Printf("Error inspecting container: %s\n", err)
        return DockerInspectResponse{Error: err}
    }
    return DockerInspectResponse{Container: &resp}
}




func (d *Docker) Remove(containerID string) DockerResult {
	
	ctx := context.Background()
	// resp
	err := d.Client.ContainerRemove(ctx,containerID,container.RemoveOptions{
		RemoveVolumes: true,
		RemoveLinks: false,
		Force: false,
	})
	if err != nil {
		log.Printf("Error removing container %s: %v\n", containerID, err)
		return DockerResult{Error: err}
	}
	return DockerResult{Action: "remove", Result: "success", Error: nil}
}