package docker

import (
	"golang.org/x/net/context"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/fsouza/go-dockerclient"
	"github.com/iron-io/titan/runner/agent"
)

// wrap docker client calls so we can retry 500s, kind of sucks but fsouza doesn't
// bake in retries we can use internally, could contribute it at some point, would
// be much more convenient if we didn't have to do this, but it's better than ad hoc retries.
// also adds timeouts to many operations, varying by operation
// TODO could generate this, maybe not worth it, may not change often
type dockerClient interface {
	// Each of these are github.com/fsouza/go-dockerclient methods

	AttachToContainer(opts docker.AttachToContainerOptions) error
	WaitContainer(id string) (int, error)
	StartContainer(id string, hostConfig *docker.HostConfig) error
	CreateContainer(opts docker.CreateContainerOptions) (*docker.Container, error)
	RemoveContainer(opts docker.RemoveContainerOptions) error
	PullImage(opts docker.PullImageOptions, auth docker.AuthConfiguration) error
	InspectImage(name string) (*docker.Image, error)
	InspectContainer(id string) (*docker.Container, error)
	StopContainer(id string, timeout uint) error
	Stats(opts docker.StatsOptions) error
}

// TODO: switch to github.com/docker/engine-api
func newClient() dockerClient {
	// docker, err := docker.NewClient(conf.Docker)
	client, err := docker.NewClientFromEnv()
	if err != nil {
		logrus.WithError(err).Fatal("couldn't create docker client")
	}

	client.SetTimeout(30 * time.Second) // TODO test w/ large dockers image pulls

	return &dockerWrap{client}
}

type dockerWrap struct {
	docker *docker.Client
}

func retry(f func() error) {
	var b agent.Backoff
	then := time.Now()
	for time.Now().Sub(then) < 2*time.Minute { // retry for 2 minutes
		err := f()
		if isTemporary(err) || isDocker500(err) {
			logrus.WithError(err).Warn("docker temporary error, retrying")
			b.Sleep()
			continue
		}
		return
	}
	logrus.Warn("retrying on docker errors exceeded 2 minutes, restart docker or rotate this instance?")
}

func isTemporary(err error) bool {
	terr, ok := err.(interface {
		Temporary() bool
	})
	return ok && terr.Temporary()
}

func isDocker500(err error) bool {
	derr, ok := err.(*docker.Error)
	return ok && derr.Status >= 500
}

func (d *dockerWrap) AttachToContainer(opts docker.AttachToContainerOptions) (err error) {
	retry(func() error {
		err = d.docker.AttachToContainer(opts)
		return err
	})
	return err
}

func (d *dockerWrap) WaitContainer(id string) (c int, err error) {
	retry(func() error {
		c, err = d.docker.WaitContainer(id)
		return err
	})
	return c, err
}

func (d *dockerWrap) StartContainer(id string, hostConfig *docker.HostConfig) (err error) {
	retry(func() error {
		err = d.docker.StartContainer(id, hostConfig)
		return err
	})
	return err
}

func (d *dockerWrap) CreateContainer(opts docker.CreateContainerOptions) (c *docker.Container, err error) {
	retry(func() error {
		c, err = d.docker.CreateContainer(opts)
		return err
	})
	return c, err
}

func (d *dockerWrap) RemoveContainer(opts docker.RemoveContainerOptions) (err error) {
	retry(func() error {
		err = d.docker.RemoveContainer(opts)
		return err
	})
	return err
}

func (d *dockerWrap) PullImage(opts docker.PullImageOptions, auth docker.AuthConfiguration) (err error) {
	retry(func() error {
		opts.Context, _ = context.WithTimeout(context.Background(), 60*time.Minute)
		err = d.docker.PullImage(opts, auth)
		return err
	})
	return err
}

func (d *dockerWrap) InspectImage(name string) (i *docker.Image, err error) {
	retry(func() error {
		i, err = d.docker.InspectImage(name)
		return err
	})
	return i, err
}

func (d *dockerWrap) InspectContainer(id string) (c *docker.Container, err error) {
	retry(func() error {
		c, err = d.docker.InspectContainer(id)
		return err
	})
	return c, err
}

func (d *dockerWrap) StopContainer(id string, timeout uint) (err error) {
	retry(func() error {
		err = d.docker.StopContainer(id, timeout)
		return err
	})
	return err
}

func (d *dockerWrap) Stats(opts docker.StatsOptions) (err error) {
	retry(func() error {
		err = d.docker.Stats(opts)
		return err
	})
	return err
}
