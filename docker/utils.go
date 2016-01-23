package docker

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/iron-io/docker-job/api/models"
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
	args = append(args, "-e", "PAYLOAD="+job.Payload)
	args = append(args, job.Image)
	cmd := exec.Command("docker", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	var b bytes.Buffer
	buff := bufio.NewWriter(&b)

	go io.Copy(buff, stdout)
	go io.Copy(buff, stderr)

	log.Printf("Waiting for command to finish...")
	if err = cmd.Wait(); err != nil {
		log.Fatal(err)
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
		fmt.Println("Image not found locally, will pull.", err)
		err = execAndPrint("docker", []string{"pull", image})
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

	log.Info("Waiting for cmd to finish...")
	err = cmd.Wait()
	log.Debugln("stderr:", berr.String())
	log.Debugln("stdout:", bout.String())
	return err
}
