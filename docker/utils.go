package docker

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/titan/api/models"
	"io"
	"os/exec"
)

func DockerRun(job *models.Job) (string, error) {
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

	log.Printf("Waiting for command to finish...")
	if err = cmd.Wait(); err != nil {
		log.Errorln("Error on cmd.wait", err)
	}
	log.Printf("Command finished with error: %v", err)
	buff.Flush()
	log.Infoln("Docker ran successfully:", b.String())
	return b.String(), nil
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
