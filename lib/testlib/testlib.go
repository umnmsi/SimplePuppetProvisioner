package testlib

import (
	"bytes"
	"fmt"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Create a temp git repo with the nodes fixtures
func CreateTestNodesRepo(fixturesDir string) (string, error) {
	files, err := ioutil.ReadDir(fixturesDir)
	if err != nil {
		return "", err
	}
	nodesDir, err := ioutil.TempDir("", "nodes")
	if err != nil {
		return "", err
	}
	rNode, err := git.PlainInit(nodesDir, false)
	if err != nil {
		return "", err
	}
	w, err := rNode.Worktree()
	if err != nil {
		return "", err
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".yaml") {
			input, err := ioutil.ReadFile(filepath.Join(fixturesDir, file.Name()))
			if err != nil {
				return "", err
			}
			err = ioutil.WriteFile(filepath.Join(nodesDir, file.Name()), input, 0644)
			if err != nil {
				return "", err
			}
			_, err = w.Add(file.Name())
			if err != nil {
				return "", err
			}
		}
		_, err = w.Commit("Add test node config files", &git.CommitOptions{
			Author: &object.Signature{
				Name:  "SimplePuppetProvisioner",
				Email: "root@msi.umn.edu",
				When:  time.Now(),
			},
		})
		if err != nil {
			return "", err
		}
	}
	_, err = rNode.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{fmt.Sprintf("file://%s", w.Filesystem.Root())},
	})
	return nodesDir, nil
}

func CheckLog(t *testing.T, regexes []*regexp.Regexp, logBuf *bytes.Buffer) {
	lines := strings.Split(logBuf.String(), "\n")
	unknown := []string{}
	for _, line := range lines {
		if line == "" {
			continue
		}
		known := false
		for _, re := range regexes {
			if re.MatchString(line) {
				known = true
				break
			}
		}
		if !known {
			unknown = append(unknown, line)
		}
	}
	for _, re := range regexes {
		found := false
		for _, line := range lines {
			if re.MatchString(line) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected log to contain '%s', but got %s\n", re, logBuf.String())
		}
	}
	if len(unknown) > 0 {
		t.Errorf("Unknown lines printed to log: %s\n", strings.Join(unknown, "\n"))
	}
}
