package nodeconfig

import (
	"fmt"
	"github.com/go-git/go-git"
	"github.com/go-git/go-git/config"
	"github.com/go-git/go-git/plumbing"
	"github.com/go-git/go-git/plumbing/object"
	gitssh "github.com/go-git/go-git/plumbing/transport/ssh"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/genericexec"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/githubwebhook"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/puppetconfig"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
	"html"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

type RepositoryInterface interface {
	Push(o *git.PushOptions) error
	ResolveRevision(rev plumbing.Revision) (*plumbing.Hash, error)
}

type WorktreeInterface interface {
	Add(path string) (plumbing.Hash, error)
	Commit(msg string, opts *git.CommitOptions) (plumbing.Hash, error)
	Pull(o *git.PullOptions) error
	Reset(opts *git.ResetOptions) error
}

type NodeClassifierInterface interface {
	Classify(hostname, environment, primary_role string, missingOK bool, requestorName, requestorEmail string) <-chan NodeConfigResult
	GetClassification(node string, missingOK bool) (*NodeConfig, error)
	GetEnvironments() EnvironmentsMsg
	GetRoles(environment string) RolesMsg
}

type NodeClassifier struct {
	nodesDir        string
	nodesPublicKeys *gitssh.PublicKeys
	nodesRepository RepositoryInterface
	nodesWorktree   WorktreeInterface
	puppetConfig    *puppetconfig.PuppetConfig
	log             *log.Logger
	notifyCallback  func(message string)
	queue           chan nodeConfigQueueMessage
	webhookTimeout  time.Duration
	execTimeout     time.Duration

	commitWatchQueue []commitWatchMessage
	commitMutex      sync.Mutex

	uuidWatchQueue chan uuidWatchMessage
	seenUuids      []genericexec.GenericExecResult
	uuidWatchers   []uuidWatchMessage
	uuidWatchCount int
	uuidMutex      sync.Mutex

	ResultChan    chan NodeConfigResult
	ListenerChans []reflect.SelectCase
}

type NodeConfig struct {
	Node        string
	Environment string
	PrimaryRole string
	RawContent  *yaml.Node
	EnvNode     *yaml.Node
	ParamsIndex int
	ParamsNode  *yaml.Node
	RoleIndex   int
	RoleNode    *yaml.Node
}

type nodeConfigQueueMessage struct {
	hostname       string
	environment    string
	primary_role   string
	requestorName  string
	requestorEmail string
	missingOK      bool
	resultChan     chan<- NodeConfigResult
}

type NodeConfigResult struct {
	Action      string
	Success     bool
	Message     string
	Node        string
	Environment string
	PrimaryRole string
}

type uuidWatchMessage struct {
	uuid       string
	resultChan chan<- genericexec.GenericExecResult
}

type commitWatchMessage struct {
	commit     string
	resultChan chan<- githubwebhook.GitHubWebhookResult
}

type EnvironmentsMsg struct {
	Status       string
	Message      string
	Environments []string
}

type RolesMsg struct {
	Status  string
	Message string
	Roles   []string
}

func NewNodeClassifier(nodesDir, nodesPrivateKey, nodesGitUser string, puppetConfig *puppetconfig.PuppetConfig, log *log.Logger, notifyCallback func(message string), webhookTimeout int64, execTimeout int64) (*NodeClassifier, error) {
	if nodesDir == "" {
		return nil, fmt.Errorf("Missing required NodesDir config option")
	}
	if nodesPrivateKey == "" {
		return nil, fmt.Errorf("Missing required NodesPrivateKey config option")
	}
	if nodesGitUser == "" {
		return nil, fmt.Errorf("Missing required NodesGitUser config option")
	}
	r, err := git.PlainOpen(nodesDir)
	if err != nil {
		return nil, fmt.Errorf("Failed to open nodes dir %s as git repository: %s", nodesDir, err)
	}
	w, err := r.Worktree()
	if err != nil {
		return nil, fmt.Errorf("Failed to set worktree for %s: %s", nodesDir, err)
	}
	sshKey, err := ioutil.ReadFile(nodesPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("Failed to open NodesPrivateKey %s: %s", nodesPrivateKey, err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(sshKey))
	if err != nil {
		return nil, fmt.Errorf("Failed to parse NodesPrivateKey %s: %s", nodesPrivateKey, err)
	}
	nodeClassifier := NodeClassifier{
		nodesDir:        nodesDir,
		nodesPublicKeys: &gitssh.PublicKeys{User: nodesGitUser, Signer: signer},
		nodesRepository: r,
		nodesWorktree:   w,
		puppetConfig:    puppetConfig,
		log:             log,
		notifyCallback:  notifyCallback,
		webhookTimeout:  time.Duration(webhookTimeout) * time.Second,
		execTimeout:     time.Duration(execTimeout) * time.Second,
		seenUuids:       []genericexec.GenericExecResult{},
		uuidWatchers:    []uuidWatchMessage{},
	}
	nodeClassifier.queue = make(chan nodeConfigQueueMessage, 50)
	nodeClassifier.uuidWatchQueue = make(chan uuidWatchMessage, 1)
	nodeClassifier.ResultChan = make(chan NodeConfigResult, 50)
	nodeClassifier.ListenerChans = []reflect.SelectCase{
		{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(make(chan githubwebhook.GitHubWebhookResult, 1)),
		},
		{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(make(chan genericexec.GenericExecResult, 1)),
		},
		{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(nodeClassifier.uuidWatchQueue),
		},
	}
	log.Printf("Using nodes directory %s for classification\n", nodesDir)
	log.Printf("Using nodes directory private key %s and user %s for git auth\n", nodesPrivateKey, nodesGitUser)
	log.Printf("Classifier GitHub Webhook timeout: %v, Exec timeout %v\n", nodeClassifier.webhookTimeout, nodeClassifier.execTimeout)
	go nodeClassifier.queueConsumer()
	go nodeClassifier.eventWatcher()
	return &nodeClassifier, nil
}

func (ctx *NodeClassifier) Classify(hostname, environment, primary_role string, missingOK bool, requestorName, requestorEmail string) <-chan NodeConfigResult {
	resultChan := make(chan NodeConfigResult, 1)
	ctx.queue <- nodeConfigQueueMessage{
		hostname:       hostname,
		environment:    environment,
		primary_role:   primary_role,
		missingOK:      missingOK,
		requestorName:  requestorName,
		requestorEmail: requestorEmail,
		resultChan:     resultChan,
	}
	return resultChan
}

func getKind(node *yaml.Node) string {
	switch node.Kind {
	case yaml.DocumentNode:
		return "DocumentNode"
	case yaml.SequenceNode:
		return "SequenceNode"
	case yaml.MappingNode:
		return "MappingNode"
	case yaml.ScalarNode:
		return "ScalarNode"
	case yaml.AliasNode:
		return "AliasNode"
	default:
		return "unknown"
	}
}

func (ctx *NodeClassifier) GetClassification(node string, missingOK bool) (*NodeConfig, error) {
	node_config := &NodeConfig{Node: node}

	// Read config file
	source, err := ioutil.ReadFile(filepath.Join(ctx.nodesDir, node+".yaml"))
	if err != nil {
		if os.IsNotExist(err) && missingOK {
			return node_config, nil
		}
		return node_config, fmt.Errorf("Failed to read YAML file for %s: %s", html.EscapeString(node), err)
	}

	// Parse YAML
	node_config.RawContent = &yaml.Node{}
	err = yaml.Unmarshal(source, node_config.RawContent)
	if err != nil {
		return node_config, fmt.Errorf("Failed to parse YAML file for %s: %s", html.EscapeString(node), err)
	}

	if len(node_config.RawContent.Content) == 0 || (len(node_config.RawContent.Content) == 1 && node_config.RawContent.Content[0].Kind == yaml.ScalarNode) { // Empty config
		node_config.RawContent = nil
		return node_config, nil
	}

	if len(node_config.RawContent.Content) != 1 {
		return node_config, fmt.Errorf("Unknown YAML config format for %s. Expected single Node, got %#v\n", html.EscapeString(node), node_config.RawContent.Content)
	}

	if node_config.RawContent.Content[0].Kind != yaml.MappingNode {
		return node_config, fmt.Errorf("Unknown YAML config format for %s. Expected top-level map, got %#v (Kind %s)\n", html.EscapeString(node), node_config.RawContent.Content[0], getKind(node_config.RawContent.Content[0]))
	}

	// Examine parsed YAML
	nodes := node_config.RawContent.Content[0].Content
	for i := 0; i < len(nodes); i++ {
		child := nodes[i]
		if i%2 == 1 || child.Kind != yaml.ScalarNode { // Only examine Scalar key nodes
			continue
		}
		switch child.Value {
		case "environment":
			if len(nodes) < i+2 || nodes[i+1].Kind != yaml.ScalarNode {
				return node_config, fmt.Errorf("Invalid YAML config. Expected scalar Node to follow environment")
			}
			node_config.EnvNode = nodes[i+1]
			node_config.Environment = nodes[i+1].Value
		case "parameters":
			params := nodes[i+1].Content
			node_config.ParamsIndex = i
			node_config.ParamsNode = nodes[i+1]
			for j := 0; j < len(params); j++ {
				param := params[j]
				if param.Kind == yaml.ScalarNode && param.Value == "primary_role" {
					if len(params) < j+2 || params[j+1].Kind != yaml.ScalarNode {
						return node_config, fmt.Errorf("Invalid YAML config. Expected scalar Node to follow primary_role")
					}
					node_config.RoleIndex = j
					node_config.RoleNode = params[j+1]
					node_config.PrimaryRole = params[j+1].Value
				}
			}
		}
	}

	return node_config, nil
}

func (ctx *NodeClassifier) updateConfig(config *NodeConfig, request nodeConfigQueueMessage) (string, string, error) {
	var changes []string
	var message string

	// Pull in changes
	err := ctx.nodesWorktree.Pull(&git.PullOptions{
		Auth: ctx.nodesPublicKeys,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		message = fmt.Sprintf("Failed to pull changes: %s", err)
		return message, "", err
	}

	// Update YAML
	if request.environment != config.Environment {
		changes = append(changes, fmt.Sprintf("environment from '%s' to '%s'", config.Environment, request.environment))
		if config.EnvNode != nil {
			config.EnvNode.SetString(request.environment)
		} else {
			node := yaml.Node{}
			err := yaml.Unmarshal([]byte(fmt.Sprintf("environment: %s\n", request.environment)), &node)
			if err != nil {
				message = fmt.Sprintf("Failed to create environment YAML: %s", err)
				return message, "", fmt.Errorf(message)
			}
			if config.RawContent == nil {
				config.RawContent = &node
			} else {
				config.RawContent.Content[0].Content = append(config.RawContent.Content[0].Content, node.Content[0].Content...)
			}
		}
	}
	if request.primary_role != config.PrimaryRole {
		changes = append(changes, fmt.Sprintf("primary_role from '%s' to '%s'", config.PrimaryRole, request.primary_role))
		if config.RoleNode != nil {
			if request.primary_role == "" { // Clear primary_role
				if len(config.ParamsNode.Content) == 2 { // primary_role only parameter, clear parameters
					config.RawContent.Content[0].Content = append(config.RawContent.Content[0].Content[:config.ParamsIndex], config.RawContent.Content[0].Content[config.ParamsIndex+2:]...)
				} else {
					config.ParamsNode.Content = append(config.ParamsNode.Content[:config.RoleIndex], config.ParamsNode.Content[config.RoleIndex+2:]...)
				}
			} else {
				config.RoleNode.SetString(request.primary_role)
			}
		} else {
			node := yaml.Node{}
			if config.ParamsNode != nil {
				err := yaml.Unmarshal([]byte(fmt.Sprintf("primary_role: %s\n", request.primary_role)), &node)
				if err != nil {
					message = fmt.Sprintf("Failed to create primary_role YAML: %s", err)
					return message, "", fmt.Errorf(message)
				}
				config.ParamsNode.Content = append(config.ParamsNode.Content, node.Content[0].Content...)
			} else {
				err := yaml.Unmarshal([]byte(fmt.Sprintf("parameters:\n  primary_role: %s\n", request.primary_role)), &node)
				if err != nil {
					message = fmt.Sprintf("Failed to create parameters/primary_role YAML: %s", err)
					return message, "", fmt.Errorf(message)
				}
				if config.RawContent == nil {
					config.RawContent = &node
				} else {
					config.RawContent.Content[0].Content = append(config.RawContent.Content[0].Content, node.Content[0].Content...)
				}
			}
		}
	}

	config.Environment = request.environment
	config.PrimaryRole = request.primary_role

	if len(changes) == 0 {
		return fmt.Sprintf("Node '%s' already classified as requested", config.Node), "", nil
	}

	// Write config file
	config_file := config.Node + ".yaml"
	f, err := os.OpenFile(filepath.Join(ctx.nodesDir, config_file), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		message = fmt.Sprintf("Failed to open config file %s for %s: %s", filepath.Join(ctx.nodesDir, config_file), html.EscapeString(config.Node), err)
		return message, "", fmt.Errorf(message)
	}
	encoder := yaml.NewEncoder(f)
	encoder.SetIndent(2)
	encoder.Encode(config.RawContent)

	// Commit change
	if request.requestorName == "" {
		request.requestorName = `SimplePuppetProvisioner`
	}
	if request.requestorEmail == "" {
		request.requestorEmail = `msi-githubuser@umn.edu`
	}
	_, err = ctx.nodesWorktree.Add(config_file)
	if err != nil {
		message = fmt.Sprintf("Failed to git add %s: %s", config_file, err)
		return message, "", fmt.Errorf(message)
	}
	commit, err := ctx.nodesWorktree.Commit(fmt.Sprintf("Autoprovision %s", config.Node), &git.CommitOptions{
		Author: &object.Signature{
			Name:  request.requestorName,
			Email: request.requestorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		message = fmt.Sprintf("Failed to commit %s: %s", config_file, err)
		return message, "", fmt.Errorf(message)
	}

	return fmt.Sprintf("Updated classification for '%s': Changed %s", config.Node, strings.Join(changes, " and ")), commit.String(), nil
}

func (ctx *NodeClassifier) pushChanges() error {
	return ctx.nodesRepository.Push(&git.PushOptions{
		Auth:       ctx.nodesPublicKeys,
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{"refs/heads/master:refs/heads/master"},
	})
}

func (ctx *NodeClassifier) GetEnvironments() EnvironmentsMsg {
	var environments []string
	for _, path := range ctx.puppetConfig.EnvironmentPath {
		dirs, err := ioutil.ReadDir(path)
		if err != nil {
			return EnvironmentsMsg{"ERROR", fmt.Sprintf("Failed to read environment path %s: %s", path, err), environments}
		} else {
			for _, environment := range dirs {
				if !strings.HasPrefix(environment.Name(), ".") {
					environments = append(environments, environment.Name())
				}
			}
		}
	}
	return EnvironmentsMsg{"OK", "", environments}
}

func (ctx *NodeClassifier) GetRoles(environment string) RolesMsg {
	roles := []string{}
	role_path := ""
PathLoop:
	for _, path := range ctx.puppetConfig.EnvironmentPath {
		dirs, err := ioutil.ReadDir(path)
		if err != nil {
			return RolesMsg{"ERROR", fmt.Sprintf("Failed to read environment path %s: %s", path, err), roles}
		} else {
			for _, dir := range dirs {
				if dir.Name() == environment {
					role_path = filepath.Join(path, dir.Name(), "site", "role", "manifests")
					break PathLoop
				}
			}
		}
	}
	if role_path == "" {
		return RolesMsg{"ERROR", fmt.Sprintf("Failed to find environment directory for '%s'", environment), roles}
	}
	files, err := ioutil.ReadDir(role_path)
	if err != nil {
		return RolesMsg{"ERROR", fmt.Sprintf("Failed to read role path %s: %s", role_path, err), roles}
	} else {
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".pp") {
				roles = append(roles, strings.TrimSuffix(file.Name(), ".pp"))
			}
		}
	}
	return RolesMsg{"OK", "", roles}
}

func (ctx *NodeClassifier) queueConsumer() {
	for message, opened := <-ctx.queue; opened; message, opened = <-ctx.queue {
		resultChan := message.resultChan
		if resultChan == nil {
			continue
		}
		ctx.log.Printf("Processing classification for %s\n", message.hostname)
		ctx.notify(fmt.Sprintf("Processing classification for %s", message.hostname))
		config, err := ctx.GetClassification(message.hostname, message.missingOK)
		if err != nil {
			result := NodeConfigResult{
				Action:  "classify",
				Success: false,
				Message: fmt.Sprintf("Failed to classify %s: %s", message.hostname, err),
				Node:    message.hostname,
			}
			ctx.sendResult(resultChan, result)
			close(resultChan)
			continue
		}
		result, commit, err := ctx.updateConfig(config, message)
		chanResult := NodeConfigResult{
			Action:  "classify",
			Success: true,
			Node:    message.hostname,
			Message: result,
		}
		ctx.log.Println(result)
		ctx.notify(result)
		if err != nil {
			chanResult.Success = false
		} else if commit != "" {
			ctx.uuidMutex.Lock()
			ctx.uuidWatchCount++
			ctx.uuidMutex.Unlock()
			webhookResultChan := make(chan githubwebhook.GitHubWebhookResult)
			ctx.commitMutex.Lock()
			commitWatcher := commitWatchMessage{
				commit:     commit,
				resultChan: webhookResultChan,
			}
			ctx.commitWatchQueue = append(ctx.commitWatchQueue, commitWatcher)
			ctx.commitMutex.Unlock()
			err = ctx.pushChanges()
			if err != nil {
				msg := fmt.Sprintf("Failed to push changes for %s: %s", message.hostname, err)
				ctx.log.Println(msg)
				ctx.notify(msg)
				chanResult.Success = false
				chanResult.Message = chanResult.Message + ". " + msg
				ctx.removeWatchers(&commitWatcher, nil)
			} else {
				go func() {
					select {
					case webhookResult := <-webhookResultChan:
						execResultChan := make(chan genericexec.GenericExecResult)
						uuidWatcher := uuidWatchMessage{
							uuid:       webhookResult.UUID,
							resultChan: execResultChan,
						}
						ctx.uuidWatchQueue <- uuidWatcher
						select {
						case execResult := <-execResultChan:
							msg := execResult.Message
							if execResult.ExitCode != 0 {
								chanResult.Message = chanResult.Message + ", but there was a non-zero exit code during deploy: " + execResult.Message
								msg = fmt.Sprintf("There was a non-zero exit code during deploy for node '%s': %s", message.hostname, execResult.Message)
								ctx.notify(msg)
							} else {
								chanResult.Message = chanResult.Message + ". " + execResult.Message
							}
							chanResult.Environment = message.environment
							chanResult.PrimaryRole = message.primary_role
							ctx.log.Println(msg)
						case <-time.After(ctx.execTimeout):
							chanResult.Message = fmt.Sprintf("%s, but there was a timeout waiting for deploy script for node '%s'", chanResult.Message, message.hostname)

							msg := fmt.Sprintf("There was a timeout waiting for deploy script for node '%s'", message.hostname)
							ctx.log.Println(msg)
							ctx.notify(msg)
							ctx.removeWatchers(nil, &uuidWatcher)
						}
					case <-time.After(ctx.webhookTimeout):
						chanResult.Message = fmt.Sprintf("%s, but there was a timeout waiting for github webhook for node '%s'", chanResult.Message, message.hostname)
						msg := fmt.Sprintf("There was a timeout waiting for github webhook for node '%s'", message.hostname)
						ctx.log.Println(msg)
						ctx.notify(msg)
						ctx.removeWatchers(&commitWatcher, nil)
					}
					ctx.sendResult(resultChan, chanResult)
					close(resultChan)
				}()
				continue
			}
		}
		ctx.sendResult(resultChan, chanResult)
		close(resultChan)
	}
}

func (ctx *NodeClassifier) removeWatchers(commitWatcher *commitWatchMessage, uuidWatcher *uuidWatchMessage) {
	if commitWatcher != nil {
		for i, message := range ctx.commitWatchQueue {
			if message.commit == commitWatcher.commit {
				ctx.commitMutex.Lock()
				ctx.commitWatchQueue = append(ctx.commitWatchQueue[:i], ctx.commitWatchQueue[i+1:]...)
				ctx.commitMutex.Unlock()
				break
			}
		}
		ctx.uuidMutex.Lock()
		ctx.uuidWatchCount--
		ctx.uuidMutex.Unlock()
	}
	if uuidWatcher != nil {
		for i, message := range ctx.uuidWatchers {
			if message.uuid == uuidWatcher.uuid {
				ctx.uuidMutex.Lock()
				ctx.uuidWatchers = append(ctx.uuidWatchers[:i], ctx.uuidWatchers[i+1:]...)
				ctx.uuidWatchCount--
				ctx.uuidMutex.Unlock()
				break
			}
		}
	}
	ctx.uuidMutex.Lock()
	if ctx.uuidWatchCount == 0 {
		ctx.seenUuids = nil
	}
	ctx.uuidMutex.Unlock()
}

func (ctx *NodeClassifier) notify(message string) {
	ctx.notifyCallback(message)
}

func (ctx *NodeClassifier) sendResult(resultChan chan<- NodeConfigResult, result NodeConfigResult) {
	ctx.ResultChan <- result
	resultChan <- result
}

func (ctx *NodeClassifier) eventWatcher() {
	for {
		index, rvalue, ok := reflect.Select(ctx.ListenerChans)
		if !ok {
			ctx.ListenerChans[index].Chan = reflect.ValueOf(nil)
			continue
		}
		switch value := rvalue.Interface().(type) {
		case githubwebhook.GitHubWebhookResult:
			for _, message := range ctx.commitWatchQueue {
				for i, commit := range value.Commits {
					if commit == message.commit {
						ctx.commitMutex.Lock()
						ctx.commitWatchQueue = append(ctx.commitWatchQueue[:i], ctx.commitWatchQueue[i+1:]...)
						ctx.commitMutex.Unlock()
						message.resultChan <- value
						break
					}
				}
			}
		case genericexec.GenericExecResult:
			if ctx.uuidWatchCount > 0 {
				ctx.uuidMutex.Lock()
				messageSent := false
				for i, uuidWatcher := range ctx.uuidWatchers {
					if uuidWatcher.uuid == value.UUID {
						ctx.uuidWatchers = append(ctx.uuidWatchers[:i], ctx.uuidWatchers[i+1:]...)
						ctx.uuidWatchCount--
						uuidWatcher.resultChan <- value
						messageSent = true
						if ctx.uuidWatchCount == 0 {
							ctx.seenUuids = nil
						}
						break
					}
				}
				if !messageSent {
					ctx.seenUuids = append(ctx.seenUuids, value)
				}
				ctx.uuidMutex.Unlock()
			}
		case uuidWatchMessage:
			ctx.uuidMutex.Lock()
			foundMatch := false
			for _, seenUuid := range ctx.seenUuids {
				if seenUuid.UUID == value.uuid {
					ctx.uuidWatchCount--
					value.resultChan <- seenUuid
					foundMatch = true
					if ctx.uuidWatchCount == 0 {
						ctx.seenUuids = nil
					}
					break
				}
			}
			if !foundMatch {
				ctx.uuidWatchers = append(ctx.uuidWatchers, value)
			}
			ctx.uuidMutex.Unlock()
		}
	}
}
