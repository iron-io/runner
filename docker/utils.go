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

func DockerRun(job titan_go.Job) (string, error) {
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
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Errorln("Couldn't get stderr", err)
	}
	if err := cmd.Start(); err != nil {
		log.Errorln("Couldn't start container", err)
	}
	var b bytes.Buffer
	buff := bufio.NewWriter(&b)

	go io.Copy(buff, stdout)
	go io.Copy(buff, stderr)

	done := make(chan bool, 1)

	go waitCmd(cmd, done)
	go waitTimeout(cmd, buff, done, time.Duration(job.Timeout))

	isSuccess := <- done

	buff.Flush()
	if (isSuccess) {
		log.Infoln("Docker ran successfully:", b.String())
		return b.String(), nil
	} else {
		log.Infoln("Timeout:", b.String())
		return b.String(), errors.New("Timeout")
	}
}

func waitTimeout(cmd *exec.Cmd, buff *bufio.Writer, done chan bool, timeout time.Duration) {
	time.Sleep(timeout * time.Second)
	cmd.Process.Kill()
	buff.Write([]byte("Timeout"))
	done <- false
}
func waitCmd(cmd *exec.Cmd, done chan bool) {
	log.Printf("Waiting for command to finish...")
	if err := cmd.Wait(); err != nil {
		log.Errorln("Error on cmd.wait", err)
	}
	done <- true
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
