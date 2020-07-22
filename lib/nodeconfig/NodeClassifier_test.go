package nodeconfig

import (
	"bytes"
	"fmt"
	"github.com/go-git/go-git"
	"github.com/go-git/go-git/plumbing"
	"github.com/google/uuid"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/genericexec"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/githubwebhook"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/puppetconfig"
	. "github.com/umnmsi/SimplePuppetProvisioner/lib/testlib"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

type RepositoryMock struct {
	sut                  *NodeClassifier
	r                    RepositoryInterface
	shouldSleep          bool
	shouldFail           bool
	shouldTimeoutWebhook bool
	shouldTimeoutExec    bool
	shouldFailExec       bool
}

func (ctx *RepositoryMock) Push(options *git.PushOptions) error {
	if ctx.shouldFail {
		return fmt.Errorf("Failed to push")
	}
	if ctx.shouldTimeoutWebhook {
		return nil
	}
	hash, err := ctx.r.ResolveRevision("HEAD")
	if err != nil {
		return err
	}
	eventUuid := uuid.New()
	ctx.sut.ListenerChans[0].Chan.Interface().(chan githubwebhook.GitHubWebhookResult) <- githubwebhook.GitHubWebhookResult{
		Event:   "push",
		Commits: []string{hash.String()},
		UUID:    eventUuid.String(),
	}
	if ctx.shouldTimeoutExec {
		return nil
	}
	go func(shouldSleep, shouldFailExec bool) {
		if shouldSleep {
			time.Sleep(time.Second)
		}
		exitCode := 0
		message := ""
		if shouldFailExec {
			exitCode = 1
			message = "Failed to run script"
		}
		ctx.sut.ListenerChans[1].Chan.Interface().(chan genericexec.GenericExecResult) <- genericexec.GenericExecResult{
			Name:     "r10k",
			UUID:     eventUuid.String(),
			ExitCode: exitCode,
			Message:  message,
		}
	}(ctx.shouldSleep, ctx.shouldFailExec)

	return nil
}

func (ctx *RepositoryMock) ResolveRevision(rev plumbing.Revision) (*plumbing.Hash, error) {
	hash, err := ctx.r.ResolveRevision(rev)
	return hash, err
}

type WorktreeMock struct {
	w                     WorktreeInterface
	shouldPullFail        bool
	shouldPullPassthrough bool
}

func (ctx *WorktreeMock) Add(path string) (plumbing.Hash, error) {
	hash, err := ctx.w.Add(path)
	return hash, err
}

func (ctx *WorktreeMock) Commit(msg string, opts *git.CommitOptions) (plumbing.Hash, error) {
	hash, err := ctx.w.Commit(msg, opts)
	return hash, err
}

func (ctx *WorktreeMock) Pull(o *git.PullOptions) error {
	if ctx.shouldPullFail {
		return fmt.Errorf("Failed to pull: Test error")
	} else if ctx.shouldPullPassthrough {
		return ctx.w.Pull(o)
	}
	return nil
}

func (ctx *WorktreeMock) Reset(opts *git.ResetOptions) error {
	return ctx.w.Reset(opts)
}

type eventSimulator struct {
	repoMock                             *RepositoryMock
	worktreeMock                         *WorktreeMock
	sut                                  *NodeClassifier
	eventResultChans, eventListenerChans []reflect.SelectCase
}

func sutFactory(nodesDir, nodesPrivateKey, nodesGitUser string, puppetConfig *puppetconfig.PuppetConfig, notifyCallback func(message string)) (*NodeClassifier, error, *bytes.Buffer, *eventSimulator) {
	var err error
	if nodesDir == "" {
		nodesDir, err = CreateTestNodesRepo("../../TestFixtures/nodes")
		if err != nil {
			return nil, err, nil, nil
		}
	}
	if nodesPrivateKey == "" {
		nodesPrivateKey = "../../TestFixtures/id_rsa_test"
	}
	if nodesGitUser == "" {
		nodesGitUser = "git"
	}
	if puppetConfig == nil {
		puppetConfig = &puppetconfig.PuppetConfig{
			PuppetExecutable: "puppet",
			SslDir:           "/testssl",
			CsrDir:           "/testssl/csr",
			SignedCertDir:    "/testssl/cert",
			EnvironmentPath:  []string{"/env1", "/env2"},
		}
	}
	if notifyCallback == nil {
		notifyCallback = func(message string) {}
	}

	var logBuf bytes.Buffer
	testLog := log.New(&logBuf, "", 0)

	var simulator eventSimulator
	sut, err := NewNodeClassifier(nodesDir, nodesPrivateKey, nodesGitUser, puppetConfig, testLog, notifyCallback, 2, 2)
	if err == nil {
		repoMock := RepositoryMock{
			sut: sut,
			r:   sut.nodesRepository,
		}
		sut.nodesRepository = &repoMock
		worktreeMock := WorktreeMock{
			w:                     sut.nodesWorktree,
			shouldPullPassthrough: true,
		}
		sut.nodesWorktree = &worktreeMock
		eventResultChans := []reflect.SelectCase{{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(sut.ResultChan),
		}}
		simulator = eventSimulator{
			repoMock:           &repoMock,
			worktreeMock:       &worktreeMock,
			sut:                sut,
			eventResultChans:   eventResultChans,
			eventListenerChans: sut.ListenerChans,
		}
		go simulator.eventWatcher()
	}

	return sut, err, &logBuf, &simulator
}

func (ctx *eventSimulator) eventWatcher() {
	for {
		index, _, ok := reflect.Select(ctx.eventResultChans)
		if !ok {
			ctx.eventResultChans[index].Chan = reflect.ValueOf(nil)
			continue
		}
	}
}

func TestNewNodeClassifier(t *testing.T) {
	sut, err, logBuf, _ := sutFactory("", "", "", nil, nil)
	if sut == nil || err != nil {
		t.Error(err)
		t.FailNow()
	}
	defer os.RemoveAll(sut.nodesDir)
	if !strings.Contains(logBuf.String(), "Using nodes directory") {
		t.Errorf("Log did not contain 'Using nodes directory': %s", logBuf.String())
	}
}

func TestNodeClassifier_FailIfNotRepo(t *testing.T) {
	sut, err, _, _ := sutFactory("/fake", "", "", nil, nil)
	if sut != nil || err == nil {
		t.Error("Expected non-repo nodesDir to fail")
		t.FailNow()
	}
	if !strings.Contains(err.Error(), "repository does not exist") {
		t.Errorf("Expected error to contain 'repository does not exist': %s", err)
	}
}

func TestNodeClassifier_ClosedEventChanOK(t *testing.T) {
	sut, err, _, _ := sutFactory("", "", "", nil, nil)
	if sut == nil || err != nil {
		t.Error(err)
		t.FailNow()
	}
	defer os.RemoveAll(sut.nodesDir)
	sut.ListenerChans[0].Chan.Close()
	time.Sleep(time.Second)
}

func TestNewNodeClassifier_FailIfBareRepo(t *testing.T) {
	nodesDir, err := ioutil.TempDir("", "nodes")
	if err != nil {
		t.Errorf("Failed to make temp repo dir: %s", err)
		return
	}
	defer os.RemoveAll(nodesDir)
	_, err = git.PlainInit(nodesDir, true)
	if err != nil {
		t.Errorf("Failed to PlainInit repo dir: %s", err)
		return
	}
	sut, err, _, _ := sutFactory(nodesDir, "", "", nil, nil)
	if sut != nil || err == nil {
		t.Error("Expected bare nodesDir to fail")
		t.FailNow()
	}
	if !strings.Contains(err.Error(), "Failed to set worktree") {
		t.Errorf("Expected error to contain 'Failed to set worktree': %s", err)
	}
}

type ClassifyExpect struct {
	Description, Node, Environment, PrimaryRole string
	OldEnvironment, OldPrimaryRole              string
	RequestorName, RequestorEmail               string
	HasParams, MissingOK                        bool
	Content                                     string
	ErrorMsg, GetClassificationErrorMsg         string
	LogLines                                    []*regexp.Regexp
	Before, After                               func()
}

type PermErrorMessage struct {
	description string
	path        string
	mode        os.FileMode
	errMsg      string
}

func TestNodeClassifier_ClassifyParallel(t *testing.T) {
	sut, err, _, simulator := sutFactory("", "", "", nil, nil)
	if err != nil {
		t.Errorf("Failed to create NodeClassifier: %s\n", err)
		return
	}

	resultChans := []<-chan NodeConfigResult{}

	for count := 1; count <= 60; {
		simulator.repoMock.shouldSleep = true
		resultChans = append(resultChans, sut.Classify("complex", "env1", "role1", false, "", ""))
		count++
		simulator.repoMock.shouldSleep = false
		resultChans = append(resultChans, sut.Classify("complex", "env2", "role2", false, "", ""))
		count++
	}

	cases := make([]reflect.SelectCase, len(resultChans))
	for i, resultChan := range resultChans {
		cases[i] = reflect.SelectCase{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(resultChan),
		}
	}
	for results := 0; results < len(resultChans); {
		chosen, value, ok := reflect.Select(cases)
		if !ok {
			cases[chosen].Chan = reflect.ValueOf(nil)
			continue
		}
		result := value.Interface().(NodeConfigResult)
		if !result.Success {
			t.Errorf("Expected Success = true but got false, message '%s'\n", result.Message)
		}
		results++
	}

	checkCountersAndQueues(t, sut)
}

func TestNodeClassifier_Classify(t *testing.T) {
	sut, err, logBuf, simulator := sutFactory("", "", "", nil, nil)
	if sut == nil || err != nil {
		t.Error(err)
		t.FailNow()
	}

	defer os.RemoveAll(sut.nodesDir)

	expects := []ClassifyExpect{
		{
			Description:    "EmptyYamlOK",
			Node:           "emptynode",
			Environment:    "test_environment",
			OldEnvironment: "",
			PrimaryRole:    "test_role",
			OldPrimaryRole: "",
			HasParams:      true,
			Content: `environment: test_environment
parameters:
  primary_role: test_role
`,
		},
		{
			Description:               "NewNodeErrorsWithoutMissingOK",
			Node:                      "newnode",
			Environment:               "test_environment",
			PrimaryRole:               "test_role",
			MissingOK:                 false,
			ErrorMsg:                  "no such file",
			GetClassificationErrorMsg: "no such file",
		},
		{
			Description:    "NewNodeEnvAndRoleOKWhenMissingOK",
			Node:           "newnode",
			Environment:    "test_environment",
			OldEnvironment: "",
			PrimaryRole:    "test_role",
			OldPrimaryRole: "",
			HasParams:      true,
			MissingOK:      true,
			Content: `environment: test_environment
parameters:
  primary_role: test_role
`,
		},
		{
			Description:    "NewNodeRoleOKWhenMissingOK",
			Node:           "newnode",
			Environment:    "",
			OldEnvironment: "",
			PrimaryRole:    "test_role",
			OldPrimaryRole: "",
			HasParams:      true,
			MissingOK:      true,
			Content: `parameters:
  primary_role: test_role
`,
		},
		{
			Description:    "UpdateEnvAndRoleOK",
			Node:           "complex",
			Environment:    "test_environment",
			OldEnvironment: "persistent_systems_production",
			PrimaryRole:    "test_role",
			OldPrimaryRole: "api_msi_server__jobrunner",
			HasParams:      true,
			Content: `environment: test_environment
parameters:
  primary_role: test_role
# This node has cname helpdesk.msi. Allow ssh from linux workstations for acctadmin.
firewall_msi::rules::both:
  200 Accept ssh from linux workstations:
    proto: tcp
    dport: 22
    source: addressbook:Linux Workstations
    state: NEW
    action: accept
additional_classes:
- aaa_msi::ldap_backup_target
- pam_access_msi::entry::allow_techsup_all
`,
		},
		{
			Description:    "UpdateEnvRemoveRoleOK",
			Node:           "complex",
			Environment:    "test_environment",
			OldEnvironment: "persistent_systems_production",
			PrimaryRole:    "",
			OldPrimaryRole: "api_msi_server__jobrunner",
			HasParams:      false,
			Content: `environment: test_environment
# This node has cname helpdesk.msi. Allow ssh from linux workstations for acctadmin.
firewall_msi::rules::both:
  200 Accept ssh from linux workstations:
    proto: tcp
    dport: 22
    source: addressbook:Linux Workstations
    state: NEW
    action: accept
additional_classes:
- aaa_msi::ldap_backup_target
- pam_access_msi::entry::allow_techsup_all
`,
		},
		{
			Description:    "NoChangesRequiredOK",
			Node:           "complex",
			Environment:    "persistent_systems_production",
			OldEnvironment: "persistent_systems_production",
			PrimaryRole:    "api_msi_server__jobrunner",
			OldPrimaryRole: "api_msi_server__jobrunner",
			HasParams:      true,
			Content: `---
environment: persistent_systems_production
parameters:
  primary_role: api_msi_server__jobrunner

# This node has cname helpdesk.msi. Allow ssh from linux workstations for acctadmin.
firewall_msi::rules::both:
  200 Accept ssh from linux workstations:
    proto: tcp
    dport: 22
    source: addressbook:Linux Workstations
    state: NEW
    action: accept

additional_classes:
  - aaa_msi::ldap_backup_target
  - pam_access_msi::entry::allow_techsup_all
`,
		},
		{
			Description:    "UpdateEnvAndRoleExtraParamsOK",
			Node:           "complexparams",
			Environment:    "test_environment",
			OldEnvironment: "persistent_systems_production",
			PrimaryRole:    "test_role",
			OldPrimaryRole: "api_msi_server__jobrunner",
			HasParams:      true,
			Content: `environment: test_environment
parameters:
  primary_role: test_role
  extra_param1:
  - value1
  - value2
  extra_param2: value1
# This node has cname helpdesk.msi. Allow ssh from linux workstations for acctadmin.
firewall_msi::rules::both:
  200 Accept ssh from linux workstations:
    proto: tcp
    dport: 22
    source: addressbook:Linux Workstations
    state: NEW
    action: accept
additional_classes:
- aaa_msi::ldap_backup_target
- pam_access_msi::entry::allow_techsup_all
`,
		},
		{
			Description:    "UpdateEnvRemoveRoleExtraParamsOK",
			Node:           "complexparams",
			Environment:    "test_environment",
			OldEnvironment: "persistent_systems_production",
			PrimaryRole:    "",
			OldPrimaryRole: "api_msi_server__jobrunner",
			HasParams:      true,
			Content: `environment: test_environment
parameters:
  extra_param1:
  - value1
  - value2
  extra_param2: value1
# This node has cname helpdesk.msi. Allow ssh from linux workstations for acctadmin.
firewall_msi::rules::both:
  200 Accept ssh from linux workstations:
    proto: tcp
    dport: 22
    source: addressbook:Linux Workstations
    state: NEW
    action: accept
additional_classes:
- aaa_msi::ldap_backup_target
- pam_access_msi::entry::allow_techsup_all
`,
		},
		{
			Description:               "InvalidYamlErrors",
			Node:                      "invalid",
			Environment:               "test_environment",
			PrimaryRole:               "test_role",
			ErrorMsg:                  "Failed to parse YAML",
			GetClassificationErrorMsg: "Failed to parse YAML",
			Content: `---
environment: persistent_systems_production
parameters:
  primary_role: api_msi_server__jobrunner
  - invalid

# This node has cname helpdesk.msi. Allow ssh from linux workstations for acctadmin.
firewall_msi::rules::both:
  200 Accept ssh from linux workstations:
    proto: tcp
    dport: 22
    source: addressbook:Linux Workstations
    state: NEW
    action: accept

additional_classes:
  - aaa_msi::ldap_backup_target
  - pam_access_msi::entry::allow_techsup_all
`,
		},
		{
			Description:    "UpdateEnvAndAddRoleExtraParamsOK",
			Node:           "parameters",
			Environment:    "test_environment",
			OldEnvironment: "persistent_systems_production",
			PrimaryRole:    "test_role",
			OldPrimaryRole: "",
			HasParams:      true,
			Content: `environment: test_environment
parameters:
  param1: value1
  param2:
  - value1
  - value2
  param3:
    param4: value1
  primary_role: test_role
# This node has cname helpdesk.msi. Allow ssh from linux workstations for acctadmin.
firewall_msi::rules::both:
  200 Accept ssh from linux workstations:
    proto: tcp
    dport: 22
    source: addressbook:Linux Workstations
    state: NEW
    action: accept
additional_classes:
- aaa_msi::ldap_backup_target
- pam_access_msi::entry::allow_techsup_all
`,
		},
		{
			Description:    "UnsetEnvironmentAndRoleOK",
			Node:           "noenvorrole",
			Environment:    "test_environment",
			OldEnvironment: "",
			PrimaryRole:    "test_role",
			OldPrimaryRole: "",
			HasParams:      true,
			Content: `# This node has cname helpdesk.msi. Allow ssh from linux workstations for acctadmin.
firewall_msi::rules::both:
  200 Accept ssh from linux workstations:
    proto: tcp
    dport: 22
    source: addressbook:Linux Workstations
    state: NEW
    action: accept
additional_classes:
- aaa_msi::ldap_backup_target
- pam_access_msi::entry::allow_techsup_all
environment: test_environment
parameters:
  primary_role: test_role
`,
		},
		{
			Description: "InvalidEnvironmentErrors",
			Node:        "emptynode",
			Environment: "invalid: env: name",
			ErrorMsg:    "Failed to create environment YAML",
			LogLines:    []*regexp.Regexp{regexp.MustCompile("Failed to create environment YAML")},
		},
		{
			Description: "InvalidPrimaryRoleErrors",
			Node:        "emptynode",
			PrimaryRole: "invalid: role: name",
			ErrorMsg:    "Failed to create parameters/primary_role YAML",
			LogLines:    []*regexp.Regexp{regexp.MustCompile("Failed to create parameters/primary_role YAML")},
		},
		{
			Description: "InvalidPrimaryRoleExistingParamsErrors",
			Node:        "parameters",
			PrimaryRole: "invalid: role: name",
			ErrorMsg:    "Failed to create primary_role YAML",
			LogLines:    []*regexp.Regexp{regexp.MustCompile("Failed to create primary_role YAML")},
		},
		{
			Description: "PullFailErrors",
			Node:        "complex",
			Environment: "test_environment",
			ErrorMsg:    "Failed to pull",
			LogLines: []*regexp.Regexp{
				regexp.MustCompile("Failed to pull changes: Failed to pull: Test error"),
			},
			Before: func() { simulator.worktreeMock.shouldPullFail = true },
			After:  func() { simulator.worktreeMock.shouldPullFail = false },
		},
		{
			Description: "PushFailErrors",
			Node:        "complex",
			Environment: "test_environment",
			ErrorMsg:    "Failed to push",
			LogLines: []*regexp.Regexp{
				regexp.MustCompile("Updated classification"),
				regexp.MustCompile("Failed to push changes for complex: Failed to push"),
			},
			Before: func() { simulator.repoMock.shouldFail = true },
			After:  func() { simulator.repoMock.shouldFail = false },
		},
		{
			Description:    "WebhookTimeoutPrintsMessage",
			Node:           "complex",
			Environment:    "test_environment",
			OldEnvironment: "persistent_systems_production",
			OldPrimaryRole: "api_msi_server__jobrunner",
			LogLines: []*regexp.Regexp{
				regexp.MustCompile("There was a timeout waiting for github webhook for node 'complex'"),
			},
			Before: func() { simulator.repoMock.shouldTimeoutWebhook = true },
			After:  func() { simulator.repoMock.shouldTimeoutWebhook = false },
		},
		{
			Description:    "ExecTimeoutPrintsMessage",
			Node:           "complex",
			Environment:    "test_environment",
			OldEnvironment: "persistent_systems_production",
			OldPrimaryRole: "api_msi_server__jobrunner",
			LogLines: []*regexp.Regexp{
				regexp.MustCompile("There was a timeout waiting for deploy script for node 'complex'"),
			},
			Before: func() { simulator.repoMock.shouldTimeoutExec = true },
			After:  func() { simulator.repoMock.shouldTimeoutExec = false },
		},
		{
			Description:    "ExecNonZeroPrintsMessage",
			Node:           "complex",
			Environment:    "test_environment",
			OldEnvironment: "persistent_systems_production",
			OldPrimaryRole: "api_msi_server__jobrunner",
			LogLines: []*regexp.Regexp{
				regexp.MustCompile("There was a non-zero exit code during deploy for node 'complex': Failed to run script"),
			},
			Before: func() { simulator.repoMock.shouldFailExec = true },
			After:  func() { simulator.repoMock.shouldFailExec = false },
		},
	}

	resetHash, err := sut.nodesRepository.ResolveRevision("HEAD")
	if err != nil {
		t.Error("Failed to resolve 'HEAD'")
	}

	for _, expect := range expects {
		t.Run(expect.Description, func(t *testing.T) {
			if expect.After != nil {
				defer expect.After()
			}
			if expect.Before != nil {
				expect.Before()
			}
			classify(t, sut, logBuf, expect)
			sut.nodesWorktree.Reset(&git.ResetOptions{
				Commit: *resetHash,
				Mode:   git.HardReset,
			})
		})
	}

	permErrorTests := []PermErrorMessage{
		{
			description: "NotWritableConfigErrors",
			path:        filepath.Join(sut.nodesDir, "complex.yaml"),
			mode:        0400,
			errMsg:      "Failed to open config file.*permission denied",
		},
		{
			description: "NotWritableGitAddErrors",
			path:        filepath.Join(sut.nodesDir, ".git"),
			mode:        0000,
			errMsg:      "Failed to git add.*permission denied",
		},
		{
			description: "NotWritableGitCommitErrors",
			path:        filepath.Join(sut.nodesDir, ".git", "refs", "heads"),
			mode:        0000,
			errMsg:      "Failed to commit.*permission denied",
		},
	}

	shouldPullPassthroughOrig := simulator.worktreeMock.shouldPullPassthrough
	simulator.worktreeMock.shouldPullPassthrough = false
	for _, test := range permErrorTests {
		t.Run(test.description, func(t *testing.T) {
			stat, err := os.Stat(test.path)
			if err != nil {
				t.Errorf("Failed to stat path %s: %s", test.path, err)
				return
			}
			err = os.Chmod(test.path, test.mode)
			if err != nil {
				t.Errorf("Failed to chown path %s: %s", test.path, err)
				return
			}
			expect := ClassifyExpect{
				Node:     "complex",
				ErrorMsg: test.errMsg,
				LogLines: []*regexp.Regexp{regexp.MustCompile(test.errMsg)},
			}
			classify(t, sut, logBuf, expect)
			os.Chmod(test.path, stat.Mode())
			sut.nodesWorktree.Reset(&git.ResetOptions{
				Commit: *resetHash,
				Mode:   git.HardReset,
			})
		})
	}
	simulator.worktreeMock.shouldPullPassthrough = shouldPullPassthroughOrig

	checkCountersAndQueues(t, sut)
}

func checkCountersAndQueues(t *testing.T, sut *NodeClassifier) {
	t.Run("CountersZeroAndQueuesEmpty", func(t *testing.T) {
		if len(sut.commitWatchQueue) != 0 {
			t.Errorf("commitWatchQueue not empty: %#v\n", sut.commitWatchQueue)
		}
		if len(sut.uuidWatchQueue) != 0 {
			t.Errorf("uuidWatchQueue not empty: %#v\n", sut.uuidWatchQueue)
		}
		if len(sut.seenUuids) != 0 {
			t.Errorf("seenUuids not empty: %#v\n", sut.seenUuids)
		}
		if len(sut.uuidWatchers) != 0 {
			t.Errorf("uuidWatchers not empty: %#v\n", sut.uuidWatchers)
		}
		if sut.uuidWatchCount != 0 {
			t.Errorf("Expected uuidWatchCount = 0, got %d", sut.uuidWatchCount)
		}
	})
}

func classify(t *testing.T, sut *NodeClassifier, logBuf *bytes.Buffer, expect ClassifyExpect) {
	logBuf.Reset()

	resultChan := sut.Classify(expect.Node, expect.Environment, expect.PrimaryRole, expect.MissingOK, expect.RequestorName, expect.RequestorEmail)
	select {
	case result := <-resultChan:
		if expect.ErrorMsg != "" {
			if result.Success || !regexp.MustCompile(expect.ErrorMsg).MatchString(result.Message) {
				t.Errorf("Expected classify to fail with error message '%s', but got Success '%t', Message '%s'", expect.ErrorMsg, result.Success, result.Message)
				return
			}
		} else if !result.Success {
			t.Errorf("Expected Success = true but got false, message '%s'\n", result.Message)
			return
		}
	case <-time.After(5 * time.Second):
		t.Error("Timeout waiting for classification")
		return
	}

	expectedLogLines := append(expect.LogLines,
		regexp.MustCompile(`^\Q`+fmt.Sprintf("Processing classification for %s", expect.Node)+`\E$`),
	)

	if expect.ErrorMsg == "" {
		validateConfig(sut, t, GetClassificationExpect{
			Node:        expect.Node,
			Environment: expect.Environment,
			PrimaryRole: expect.PrimaryRole,
			HasParams:   expect.HasParams,
			ErrorMsg:    expect.GetClassificationErrorMsg,
		})

		var expectedChanges []string
		if expect.OldEnvironment != expect.Environment {
			expectedChanges = append(expectedChanges, fmt.Sprintf("environment from '%s' to '%s'", expect.OldEnvironment, expect.Environment))
		}
		if expect.OldPrimaryRole != expect.PrimaryRole {
			expectedChanges = append(expectedChanges, fmt.Sprintf("primary_role from '%s' to '%s'", expect.OldPrimaryRole, expect.PrimaryRole))
		}
		if len(expectedChanges) > 0 {
			expectedMsg := fmt.Sprintf("Updated classification for '%s': Changed %s", expect.Node, strings.Join(expectedChanges, " and "))
			expectedLogLines = append(expectedLogLines, regexp.MustCompile(`^\Q`+expectedMsg+`\E$`))
		} else if expect.ErrorMsg == "" {
			expectedMsg := fmt.Sprintf("Node '%s' already classified as requested", expect.Node)
			expectedLogLines = append(expectedLogLines, regexp.MustCompile(`^\Q`+expectedMsg+`\E$`))
		}
	}

	CheckLog(t, expectedLogLines, logBuf)

	if expect.Content != "" {
		source, err := ioutil.ReadFile(filepath.Join(sut.nodesDir, expect.Node+".yaml"))
		if err != nil {
			t.Errorf("Failed to read nodes file %s: %s", expect.Node+".yaml", err)
		} else {
			content := fmt.Sprintf("%s", source)
			if content != expect.Content {
				t.Errorf("Expected config file content to be '%s', got '%s'\n", expect.Content, content)
			}
		}
	}
}

type GetClassificationExpect struct {
	Description, Node, Environment, PrimaryRole string
	HasParams                                   bool
	MissingOK                                   bool
	ErrorMsg                                    string
}

func TestNodeClassifier_GetClassification(t *testing.T) {
	sut, err, logBuf, _ := sutFactory("", "", "", nil, nil)
	if sut == nil || err != nil {
		t.Error(err)
		t.FailNow()
	}

	defer os.RemoveAll(sut.nodesDir)

	expects := []GetClassificationExpect{
		{
			Description: "NotExistsErrors",
			Node:        "missingnode",
			Environment: "",
			PrimaryRole: "",
			ErrorMsg:    "no such file or directory",
		},
		{
			Description: "NotExistsOKWhenMissingOK",
			Node:        "missingnode",
			Environment: "",
			PrimaryRole: "",
			MissingOK:   true,
		},
		{
			Description: "EmptyYamlOK",
			Node:        "emptynode",
			Environment: "",
			PrimaryRole: "",
		},
		{
			Description: "NoRoleOK",
			Node:        "norole",
			Environment: "persistent_systems_production",
			PrimaryRole: "",
		},
		{
			Description: "ParametersNoRoleOK",
			Node:        "parameters",
			Environment: "persistent_systems_production",
			PrimaryRole: "",
			HasParams:   true,
		},
		{
			Description: "RoleAndParamsOK",
			Node:        "complex",
			Environment: "persistent_systems_production",
			PrimaryRole: "api_msi_server__jobrunner",
			HasParams:   true,
		},
		{
			Description: "InvalidYamlErrors",
			Node:        "invalid",
			Environment: "",
			PrimaryRole: "",
			ErrorMsg:    "Failed to parse YAML",
		},
		{
			Description: "InvalidKindArrayErrors",
			Node:        "invalidarray",
			Environment: "",
			PrimaryRole: "",
			ErrorMsg:    "Unknown YAML config format",
		},
		{
			Description: "InvalidEnvironmentErrors",
			Node:        "invalidenv",
			Environment: "",
			PrimaryRole: "",
			ErrorMsg:    "Invalid YAML config",
		},
		{
			Description: "InvalidRoleErrors",
			Node:        "invalidrole",
			Environment: "",
			PrimaryRole: "",
			HasParams:   true,
			ErrorMsg:    "Invalid YAML config",
		},
	}

	for _, expect := range expects {
		t.Run(expect.Description, func(t *testing.T) {
			validateConfig(sut, t, expect)
		})
	}

	t.Run("NotReadableErrors", func(t *testing.T) {
		err := os.Chmod(filepath.Join(sut.nodesDir, "complex.yaml"), 000)
		if err != nil {
			t.Errorf("Failed to chown test file: %s", err)
			return
		}
		validateConfig(sut, t, GetClassificationExpect{
			Node:     "complex",
			ErrorMsg: "Failed to read YAML file for complex",
		})
	})

	regexes := []*regexp.Regexp{
		regexp.MustCompile(`Classifier GitHub Webhook timeout: 2s, Exec timeout 2s`),
		regexp.MustCompile(`^Using nodes directory /[[:word:]/]+ for classification$`),
		regexp.MustCompile(`^Using nodes directory private key \.\./\.\./TestFixtures/id_rsa_test and user git for git auth$`),
	}
	CheckLog(t, regexes, logBuf)
}

func validateConfig(sut *NodeClassifier, t *testing.T, expected GetClassificationExpect) {
	config, err := sut.GetClassification(expected.Node, expected.MissingOK)
	if err != nil && (expected.ErrorMsg == "" || !strings.Contains(err.Error(), expected.ErrorMsg)) {
		t.Errorf("Received error during lookup for '%s': %s", expected.Node, err)
	} else if expected.ErrorMsg != "" && err == nil {
		t.Errorf("Expected to receive error matching '%s'", expected.ErrorMsg)
	} else if config.Node != expected.Node ||
		config.Environment != expected.Environment ||
		config.PrimaryRole != expected.PrimaryRole ||
		expected.HasParams && config.ParamsNode == nil ||
		!expected.HasParams && config.ParamsNode != nil ||
		expected.Environment != "" && config.EnvNode == nil ||
		expected.Environment == "" && config.EnvNode != nil ||
		expected.PrimaryRole != "" && config.RoleNode == nil ||
		expected.PrimaryRole == "" && config.RoleNode != nil {
		t.Errorf("Parsed node config didn't match expectation for '%s': expected %#v, got %#v", expected.Node, expected, config)
	}
}

func TestNodeClassifier_GetEnvironments(t *testing.T) {
	puppetConfig := &puppetconfig.PuppetConfig{
		PuppetExecutable: "puppet",
		SslDir:           "/testssl",
		CsrDir:           "/testssl/csr",
		SignedCertDir:    "/testssl/cert",
		EnvironmentPath:  []string{"../../TestFixtures/environments"},
	}
	sut, err, _, _ := sutFactory("", "", "", puppetConfig, nil)
	if err != nil {
		t.Errorf("Failed to create NodeClassifier: %s\n", err)
		return
	}
	result := sut.GetEnvironments()
	if result.Status != "OK" {
		t.Errorf("Failed to retrieve environments: %s\n", result.Message)
	} else if result.Environments[0] != "env1" || result.Environments[1] != "env2" {
		t.Errorf("Expected environments [env1 env2], got %v\n", result.Environments)
	}
}

func TestNodeClassifier_GetEnvironmentsPathNotExistErrors(t *testing.T) {
	sut, err, _, _ := sutFactory("", "", "", nil, nil)
	if err != nil {
		t.Errorf("Failed to create NodeClassifier: %s\n", err)
		return
	}
	result := sut.GetEnvironments()
	if result.Status != "ERROR" || !strings.Contains(result.Message, "no such file") {
		t.Errorf("Expected GetEnvironments to fail with message 'no such file', but got '%s'\n", result.Message)
	}
}

func TestNodeClassifier_GetRoles(t *testing.T) {
	puppetConfig := &puppetconfig.PuppetConfig{
		PuppetExecutable: "puppet",
		SslDir:           "/testssl",
		CsrDir:           "/testssl/csr",
		SignedCertDir:    "/testssl/cert",
		EnvironmentPath:  []string{"../../TestFixtures/environments"},
	}
	sut, err, _, _ := sutFactory("", "", "", puppetConfig, nil)
	if err != nil {
		t.Errorf("Failed to create NodeClassifier: %s\n", err)
		return
	}
	t.Run("RolesExistOK", func(t *testing.T) {
		result := sut.GetRoles("env1")
		if result.Status != "OK" {
			t.Errorf("Failed to retrieve roles for environment 'env1': %s\n", result.Message)
		} else if result.Roles[0] != "role1" || result.Roles[1] != "role2" {
			t.Errorf("Expected roles [role1 role2], got %v\n", result.Roles)
		}
	})
	t.Run("RolesEnvNotExistErrors", func(t *testing.T) {
		result := sut.GetRoles("missing")
		if result.Status != "ERROR" || !strings.Contains(result.Message, "Failed to find environment directory") {
			t.Errorf("Expected GetRoles to fail with message 'Failed to find environment directory', but got '%s'\n", result.Message)
		}
	})
	t.Run("RolesNotExistErrors", func(t *testing.T) {
		result := sut.GetRoles("env2")
		if result.Status != "ERROR" || !strings.Contains(result.Message, "Failed to read role path") {
			t.Errorf("Expected GetRoles to fail with message 'Failed to read role path', but got '%s'\n", result.Message)
		}
	})
}

func TestNodeClassifier_GetRolesEnvPathNotExistErrors(t *testing.T) {
	sut, err, _, _ := sutFactory("", "", "", nil, nil)
	if err != nil {
		t.Errorf("Failed to create NodeClassifier: %s\n", err)
		return
	}
	result := sut.GetRoles("env1")
	if result.Status != "ERROR" || !strings.Contains(result.Message, "Failed to read environment path") {
		t.Errorf("Expected GetEnvironments to fail with message 'Failed to read environment path', but got '%s'\n", result.Message)
	}
}

func TestNodeClassifier_ClosedQueueMessageOK(t *testing.T) {
	sut, err, _, _ := sutFactory("", "", "", nil, nil)
	if err != nil {
		t.Errorf("Failed to create NodeClassifier: %s\n", err)
		return
	}
	sut.queue <- nodeConfigQueueMessage{}
}

func TestNodeClassifier_MissingNodesDirErrors(t *testing.T) {
	message := "Missing required NodesDir config option"
	_, err := NewNodeClassifier("", "", "", nil, nil, nil, 0, 0)
	if err == nil {
		t.Errorf("Expected NewNodeClassifier to fail with message '%s'", message)
	} else if err.Error() != message {
		t.Errorf("Expected NewNodeClassifier to fail with message '%s', but got '%s'", message, err)
	}
}

func TestNodeClassifier_MissingNodesPrivateKeyErrors(t *testing.T) {
	message := "Missing required NodesPrivateKey config option"
	_, err := NewNodeClassifier("/nodesDir", "", "", nil, nil, nil, 0, 0)
	if err == nil {
		t.Errorf("Expected NewNodeClassifier to fail with message '%s'", message)
	} else if err.Error() != message {
		t.Errorf("Expected NewNodeClassifier to fail with message '%s', but got '%s'", message, err)
	}
}

func TestNodeClassifier_MissingNodesGitUserErrors(t *testing.T) {
	message := "Missing required NodesGitUser config option"
	_, err := NewNodeClassifier("/nodesDir", "/privateKey", "", nil, nil, nil, 0, 0)
	if err == nil {
		t.Errorf("Expected NewNodeClassifier to fail with message '%s'", message)
	} else if err.Error() != message {
		t.Errorf("Expected NewNodeClassifier to fail with message '%s', but got '%s'", message, err)
	}
}

func TestNodeClassifier_BadNodesPrivateKeyPathErrors(t *testing.T) {
	message := "Failed to open NodesPrivateKey /missingPrivateKey: open /missingPrivateKey: no such file or directory"
	_, err, _, _ := sutFactory("", "/missingPrivateKey", "", nil, nil)
	if err == nil {
		t.Errorf("Expected NewNodeClassifier to fail with message '%s'", message)
	} else if err.Error() != message {
		t.Errorf("Expected NewNodeClassifier to fail with message '%s', but got '%s'", message, err)
	}
}

func TestNodeClassifier_InvalidNodesPrivateKeyFileErrors(t *testing.T) {
	message := "Failed to parse NodesPrivateKey ../../TestFixtures/empty_id_rsa: ssh: no key found"
	_, err, _, _ := sutFactory("", "../../TestFixtures/empty_id_rsa", "", nil, nil)
	if err == nil {
		t.Errorf("Expected NewNodeClassifier to fail with message '%s'", message)
	} else if err.Error() != message {
		t.Errorf("Expected NewNodeClassifier to fail with message '%s', but got '%s'", message, err)
	}
}
