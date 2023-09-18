package puppetconfig

import (
	"bytes"
	"log"
	"os/exec"
	"regexp"
	"strings"
)

type PuppetConfigParser struct {
	log          *log.Logger
	parsedConfig *PuppetConfig
}

type PuppetConfig struct {
	PuppetExecutable       string
	PuppetServerExecutable string
	PuppetVersion          string
	ConfFile               string
	ConfDir                string
	SslDir                 string
	CsrDir                 string
	SignedCertDir          string
	EnvironmentPath        []string
}

func NewPuppetConfigParser(log *log.Logger) *PuppetConfigParser {
	return &PuppetConfigParser{
		log:          log,
		parsedConfig: &PuppetConfig{},
	}
}

func (ctx PuppetConfigParser) LoadPuppetConfig(puppetExecutable string, puppetConfDir string) *PuppetConfig {
	var output bytes.Buffer

	ctx.log.Printf("Asking \"%s\" for its version...", puppetExecutable)
	versionCmd := exec.Cmd{
		Path:   puppetExecutable,
		Args:   []string{puppetExecutable, "--version"},
		Stdout: &output,
	}
	versionCmd.Run()
	var line string
	var err error = nil
	pattern := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	line, err = output.ReadString('\n')
	if err != nil {
		ctx.log.Printf("Failed to determine Puppet version from '%s'", output.String())
		return nil
	}
	line = strings.TrimSpace(line)
	matches := pattern.FindStringSubmatch(line)
	if len(matches) == 0 {
		ctx.log.Printf("Failed to find version from '%s'", line)
		return nil
	}
	ctx.parsedConfig.PuppetVersion = matches[0]
	ctx.log.Printf("Found Puppet version %s", ctx.parsedConfig.PuppetVersion)
	output.Reset()

	ctx.log.Printf("Asking \"%s\" for its configuration...", puppetExecutable)
	// Run puppet config print on the main section, then on the server section, with server values taking precedence.
	for _, section := range []string{"main", "server"} {
		configLoader := exec.Cmd{
			Path:   puppetExecutable,
			Args:   []string{puppetExecutable, "config", "print", "--confdir", puppetConfDir, "--section", section},
			Stdout: &output,
		}
		configLoader.Run()
		ctx.parseConfig(&output)

		output.Reset()
	}

	// have we read everything we need?
	if validateParsedConfig(ctx.parsedConfig) {
		ctx.parsedConfig.PuppetExecutable = puppetExecutable
		return ctx.parsedConfig
	} else {
		ctx.log.Print("Output of \"puppet config print\" was not in the correct format.")
		ctx.log.Print("Please be sure PuppetExecutable is pointing to a working puppet installation,")
		ctx.log.Print("PuppetConfDir is pointing to the systemwide puppet configuration directory,")
		ctx.log.Print("and that the user you run spp as has permission to run puppet and read the config file.")

		return nil
	}
}

func (ctx PuppetConfigParser) parseConfig(configData *bytes.Buffer) {

	parsedConfig := ctx.parsedConfig

	var err error = nil
	var line string
	pattern := regexp.MustCompile(`^([a-zA-Z_0-9]+)\s=\s(.*)\n?$`)
	for err == nil {
		line, err = configData.ReadString('\n')

		matches := pattern.FindStringSubmatch(line)
		if len(matches) > 0 {
			name := matches[1]
			value := matches[2]

			switch name {
			case "ssldir":
				parsedConfig.SslDir = value
			case "csrdir":
				parsedConfig.CsrDir = value
			case "signeddir":
				parsedConfig.SignedCertDir = value
			case "config":
				parsedConfig.ConfFile = value
			case "confdir":
				parsedConfig.ConfDir = value
			case "environmentpath":
				parsedConfig.EnvironmentPath = strings.Split(value, ":")
			}
		}
	}
}

func validateParsedConfig(cfg *PuppetConfig) bool {
	ok := true
	ok = ok && cfg.SslDir != ""
	ok = ok && cfg.CsrDir != ""
	ok = ok && cfg.SignedCertDir != ""
	ok = ok && cfg.ConfFile != ""
	ok = ok && cfg.ConfDir != ""
	ok = ok && len(cfg.EnvironmentPath) != 0

	return ok
}
