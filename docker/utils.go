package docker

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"

	log "github.com/Sirupsen/logrus"
	titan_go "github.com/iron-io/titan_go"
	"time"
)
type RunResult struct {
	cmd *exec.Cmd
	buff *bufio.Writer
	b bytes.Buffer
}

func DockerRun(job titan_go.Job) (RunResult, error) {
	err := checkAndPull(job.Image)
	if err != nil {
		return "", errors.New(fmt.Sprintln("The image", job.Image, "could not be pulled:", err))
	}
	// TODO: convert this to use the Docker API. See Docker Jockey for examples.
	args := []string{"run", "--rm", "-i"}
	log.Warnln("Passing PAYLOAD:", job.Payload)
	args = append(args, "-e", "PAYLOAD="+job.Payload)
	args = append(args, job.Image)
	cmd := exec.Command("docker", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Errorln("Couldn't get stdout", err)
		return nil, fmt.Errorf("Couldn't get stdout %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Errorln("Couldn't get stderr", err)
		return nil, fmt.Errorf("Couldn't get stderr %v", err)
	}
	if err := cmd.Start(); err != nil {
		log.Errorln("Couldn't start container", err)
		return nil, fmt.Errorf("Couldn't start container %v", err)
	}
	var b bytes.Buffer
	buff := bufio.NewWriter(&b)

	go io.Copy(buff, stdout)
	go io.Copy(buff, stderr)

	return &RunResult{
		cmd: cmd,
		buff: buff,
		b: b,
	}, nil
}

func Wait(job titan_go.Job, result RunResult) (string, error) {

	cmd := result.cmd
	buff := result.buff
	b := result.b
	errChan := make(chan error, 1)

	go waitCmd(cmd, errChan)
	go waitTimeout(cmd, buff, errChan, time.Duration(job.Timeout))

	err := <-errChan

	buff.Flush()
	log.Infoln("Docker run finished:", b.String())
	return b.String(), err
}

func waitTimeout(cmd *exec.Cmd, buff *bufio.Writer, err chan error, timeout time.Duration) {
	time.Sleep(timeout * time.Second)
	cmd.Process.Kill()
	buff.Write([]byte("Timeout"))
	err <- errors.New("Timeout")
}
func waitCmd(cmd *exec.Cmd, errChan chan bool) {
	log.Printf("Waiting for command to finish...")
	if err := cmd.Wait(); err != nil {
		log.Errorln("Error on cmd.wait", err)
		errChan <- err
	}
	errChan <- nil
}

func checkAndPull(image string) error {
	err := execAndPrint("docker", []string{"inspect", image})
	if err != nil {
		// image does not exist, so let's pull
		log.Infoln("Image not found locally, will pull.", err)
		err = execAndPrint("docker", []string{"pull", image})
		if err != nil {
			log.Errorln("Couldn't pull image", err)
		}
	}
	return err
}

func execAndPrint(cmdstr string, args []string) error {
	var bout bytes.Buffer
	buffout := bufio.NewWriter(&bout)
	var berr bytes.Buffer
	bufferr := bufio.NewWriter(&berr)
	cmd := exec.Command(cmdstr, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go io.Copy(buffout, stdout)
	go io.Copy(bufferr, stderr)

	log.Info("Waiting for cmd to finish in execAndPrint...")
	err = cmd.Wait()
	if err != nil {
		log.Errorln("Error on cmd.Wait in execAndPrint", err)
		log.Debugln("stderr:", berr.String())
		log.Debugln("stdout:", bout.String())

	}
	return err
}
