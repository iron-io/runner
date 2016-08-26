package docker

import (
	"golang.org/x/net/context"
	"net"
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

	AttachToContainerNonBlocking(opts docker.AttachToContainerOptions) (docker.CloseWaiter, error)
	WaitContainer(ctx context.Context, id string) (int, error)
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

	// NOTE add granularity to things like pull, should not effect
	// hijacked / streaming endpoints
	client.SetTimeout(120 * time.Second)

	return &dockerWrap{client}
}

type dockerWrap struct {
	docker *docker.Client
}

func retry(f func() error) {
	var b agent.Backoff
	then := time.Now()
	limit := 10 * time.Minute
	for time.Since(then) < limit {
		err := f()
		if isTemporary(err) || isDocker500(err) || isNet(err) {
			logrus.WithError(err).Warn("docker temporary error, retrying")
			b.Sleep()
			continue
		}
		return
	}
	logrus.Warnf("retrying on docker errors exceeded %s, restart docker or rotate this instance?", limit)
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

func isNet(err error) bool {
	_, ok := err.(net.Error)
	return ok
}

func (d *dockerWrap) AttachToContainerNonBlocking(opts docker.AttachToContainerOptions) (w docker.CloseWaiter, err error) {
	retry(func() error {
		w, err = d.docker.AttachToContainerNonBlocking(opts)
		return err
	})
	return w, err
}

func (d *dockerWrap) WaitContainer(ctx context.Context, id string) (code int, err error) {
	// special one, since fsouza doesn't have context on this one and tasks can
	// take longer than 20 minutes
	for {
		// backup bail mechanism so this doesn't sit here forever
		select {
		case <-ctx.Done():
		default:
		}

		retry(func() error {
			code, err = d.docker.WaitContainer(id)
			return err
		})
		// must be 200, 404 500 or net error -- only 200 & 404 are cool, otherwise retry
		_, containerNotFound := err.(*docker.NoSuchContainer)
		dockerErr, ok := err.(*docker.Error)
		if err == nil || containerNotFound || (ok && dockerErr.Status == 404) {
			break
		}
		logrus.WithError(err).Warn("retrying wait container (this is ok)")
	}
	return code, err
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
