package puppetconfig

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"
)

func TestParser(t *testing.T) {
	var logBuf bytes.Buffer
	testLog := log.New(&logBuf, "", 0)

	var confData bytes.Buffer
	signedCertDir := "/a/b/c"
	csrDir := "/d/e/f"
	sslDir := "/g/h/i"
	config := "/some/puppet.conf"
	confdir := "/some"
	environmentPath := "/a/b/c:/d/e/f"

	for datasetId, testData := range parserDataProvider() {
		sut := NewPuppetConfigParser(testLog)
		confData.Reset()
		confData.WriteString(fmt.Sprintf(testData.template, signedCertDir, csrDir, sslDir, config, confdir, environmentPath))
		sut.parseConfig(&confData)

		if testData.expectValid {
			if sut.parsedConfig.SignedCertDir != signedCertDir {
				t.Errorf("Expected parser to identify signed cert dir %s, got %s\n", signedCertDir, sut.parsedConfig.SignedCertDir)
			}
			if sut.parsedConfig.CsrDir != csrDir {
				t.Errorf("Expected parser to identify csr dir %s, got %s\n", csrDir, sut.parsedConfig.CsrDir)
			}
			if sut.parsedConfig.SslDir != sslDir {
				t.Errorf("Expected parser to identify ssl dir %s, got %s\n", sslDir, sut.parsedConfig.SslDir)
			}
			if strings.Join(sut.parsedConfig.EnvironmentPath, ":") != environmentPath {
				t.Errorf("Expected parser to identify environment path %s, got %s\n", environmentPath, sut.parsedConfig.EnvironmentPath)
			}
		}
		if validateParsedConfig(sut.parsedConfig) != testData.expectValid {
			t.Errorf("Unexpected validity state of dataset %d\n", datasetId)
			t.FailNow()
		}
	}

}

type parserTestData struct {
	template    string
	expectValid bool
	expectLog   string
}

func parserDataProvider() []parserTestData {
	result := []parserTestData{
		{
			template: `signeddir = %s
csrdir = %s
ssldir = %s
config = %s
confdir = %s
environmentpath = %s
`,
			expectValid: true,
		},
		{
			template: `signeddir = %s
csrdir = %s
ssldir = %s
config = %s
confdir = %s
environmentpath = %s`,
			expectValid: true,
		},
		{
			template: `signeddir = %s
garbage = hi
csrdir = %s
ssldir = %s
config = %s
confdir = %s
environmentpath = %s`,
			expectValid: true,
		},
		{
			template: `blabla = %s
garbage = hi
csrdir = %s
ssldir = %s`,
			expectValid: false,
		},
	}
	return result
}
