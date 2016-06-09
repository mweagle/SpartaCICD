package concourse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/Sirupsen/logrus"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"text/template"
	"time"
)

var credentialsTemplate = `
s3-bucket: weagle
aws-region: us-west-2
s3-access-key-id: {{ .AccessKeyID }}
s3-secret-access-key: {{ .SecretAccessKey }}`

const (
	concourseTargetName   = "sparta"
	concoursePipelineName = "SpartaCICD"
	ec2MetadataServer     = "http://169.254.169.254"
)

type workflowContext struct {
	basicAuthUser     string
	basicAuthPassword string
	logger            *logrus.Logger
	repoURL           string
	pipelineRelPath   string
	localCredsPath    string
	flyCLIPath        string
	tempRepoPath      string
}
type syncStep func(ctx *workflowContext) (syncStep, error)

// Utility method to run shell command
func runOSCommand(cmd *exec.Cmd, logger *logrus.Logger) error {
	logger.WithFields(logrus.Fields{
		"Arguments": cmd.Args,
		"Dir":       cmd.Dir,
		"Path":      cmd.Path,
	}).Info("Running Command")
	outputWriter := logger.Writer()
	defer outputWriter.Close()
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter
	return cmd.Run()
}

func cloneRepo(ctx *workflowContext) (syncStep, error) {
	// Get a temp directory
	tempDir, tempDirErr := ioutil.TempDir("", "SpartaCICD")
	if nil != tempDirErr {
		return nil, tempDirErr
	}
	ctx.tempRepoPath = tempDir

	// Clone the repo
	cmd := exec.Command("git", "clone", "--depth", "1", ctx.repoURL, ctx.tempRepoPath)
	return refreshCredentials, runOSCommand(cmd, ctx.logger)
}

func refreshCredentials(ctx *workflowContext) (syncStep, error) {
	roleResponse, roleResponseErr := http.Get(fmt.Sprintf("%s/latest/meta-data/iam/security-credentials/", ec2MetadataServer))
	if roleResponseErr != nil {
		return nil, roleResponseErr
	}
	defer roleResponse.Body.Close()
	roleContent, roleContentErr := ioutil.ReadAll(roleResponse.Body)
	if nil != roleContentErr {
		return nil, roleContentErr
	}
	roleName := string(roleContent)

	// Writer the body to file
	credsResponse, credsResponseErr := http.Get(fmt.Sprintf("%s/latest/meta-data/iam/security-credentials/%s", ec2MetadataServer, roleName))
	if nil != credsResponseErr {
		return nil, credsResponseErr
	}
	var credsData struct {
		Code            string
		LastUpdated     string
		Type            string
		AccessKeyID     string
		SecretAccessKey string
		Token           string
		Expiration      string
	}
	defer credsResponse.Body.Close()

	decodeErr := json.NewDecoder(credsResponse.Body).Decode(&credsData)
	if nil != decodeErr {
		return nil, decodeErr
	}
	// Get the credentials, unmarshal them, and stuff them into the region...
	template, templateErr := template.New("credentials").Parse(credentialsTemplate)
	if nil != templateErr {
		return nil, templateErr
	}
	output := &bytes.Buffer{}
	executeErr := template.Execute(output, credsData)
	if nil != executeErr {
		return nil, fmt.Errorf("Failed to execute template: %s", executeErr.Error())
	}
	return refreshPipeline, ioutil.WriteFile(ctx.localCredsPath, output.Bytes(), 0644)
}

func refreshPipeline(ctx *workflowContext) (syncStep, error) {
	// Download the fly clients
	if _, err := os.Stat(ctx.flyCLIPath); err != nil {
		// Download it...
		flyOut, flyOutErr := os.Create(ctx.flyCLIPath)
		if nil != flyOutErr {
			return nil, flyOutErr
		}

		client := &http.Client{}
		httpReq, httpReqErr := http.NewRequest("GET", "http://localhost:8080/api/v1/cli?arch=amd64&platform=linux", nil)
		if nil != httpReqErr {
			return nil, httpReqErr
		}
		httpReq.SetBasicAuth(ctx.basicAuthUser, ctx.basicAuthPassword)
		response, responseErr := client.Do(httpReq)
		if responseErr != nil {
			return nil, responseErr
		}
		defer response.Body.Close()
		// Writer the body to file
		_, copyErr := io.Copy(flyOut, response.Body)
		flyOut.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		chmodErr := os.Chmod(ctx.flyCLIPath, 755)
		if nil != chmodErr {
			return nil, chmodErr
		}
	}
	// Run the commands to set the target, configure the pipeline
	var pipelineCommands []*exec.Cmd
	pipelineCommands = append(pipelineCommands,
		exec.Command(ctx.flyCLIPath,
			"login",
			fmt.Sprintf("--username=%s", ctx.basicAuthUser),
			fmt.Sprintf("--password=%s", ctx.basicAuthPassword),
			"-t",
			concourseTargetName,
			"-c",
			"http://localhost:8080"))

	pipelineCommands = append(pipelineCommands,
		exec.Command(ctx.flyCLIPath,
			"-t",
			concourseTargetName,
			"set-pipeline",
			"-p",
			concoursePipelineName,
			"-c",
			path.Join(ctx.tempRepoPath, ctx.pipelineRelPath),
			"-l",
			ctx.localCredsPath,
			"-n"))

	pipelineCommands = append(pipelineCommands,
		exec.Command(ctx.flyCLIPath,
			"-t",
			concourseTargetName,
			"unpause-pipeline",
			"-p",
			concoursePipelineName))
	// fly -t sparta unpause-pipeline -p SpartaCICD
	for _, eachCommand := range pipelineCommands {
		commandErr := runOSCommand(eachCommand, ctx.logger)
		if nil != commandErr {
			return nil, commandErr
		}
	}
	return nil, nil
}

// SyncIt Periodically git clone the repo, check for pipelines, and create
// new ones via the FLY cli
// Ref: https://concourse.ci/fly-cli.html
// We need to do this periodically b/c the EC2 instance requires
// credentials to be injected into the pipelines...
func SyncIt(username string, password string, logger *logrus.Logger) error {
	for {
		logger.Info("Rebuilding pipeline")
		ctx := &workflowContext{
			basicAuthUser:     username,
			basicAuthPassword: password,
			logger:            logger,
			repoURL:           "https://github.com/mweagle/SpartaCICD",
			localCredsPath:    "/home/ubuntu/aws-credentials.yml",
			flyCLIPath:        "/home/ubuntu/fly",
			pipelineRelPath:   "pipeline.yml",
		}
		for curStep := cloneRepo; curStep != nil; {
			nextStep, nextStepErr := curStep(ctx)
			if nil != nextStepErr {
				logger.Error(nextStepErr)
				break
			} else {
				curStep = nextStep
			}
		}

		if "" != ctx.tempRepoPath {
			if _, err := os.Stat(ctx.tempRepoPath); err != nil {
				removeErr := os.RemoveAll(ctx.tempRepoPath)
				if nil != removeErr {
					logger.WithFields(logrus.Fields{
						"Error": removeErr,
					}).Warn("Failed to cleanup directory")
				}
			}
		}
		time.Sleep(5 * 60 * time.Second)
	}
}
