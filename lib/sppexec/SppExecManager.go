package sppexec

import (
	"log"
	"os/exec"

	"github.com/mbaynton/SimplePuppetProvisioner/lib/puppetconfig"
	"github.com/mbaynton/go-genericexec"
)

type SppExecManager struct {
	*genericexec.GenericExecManager

	puppetConfig *puppetconfig.PuppetConfig
}

func NewSppExecManager(execTaskConfigsByName map[string]genericexec.GenericExecConfig, puppetConfig *puppetconfig.PuppetConfig, log *log.Logger, notifyCallback func(message string)) *SppExecManager {
	execManager := SppExecManager{
		GenericExecManager: genericexec.NewGenericExecManager(execTaskConfigsByName, log, notifyCallback),
		puppetConfig:       puppetConfig,
	}
	execManager.CmdFactory = execManager.puppetAwareProductionCmdFactory

	return &execManager
}

func (ctx *SppExecManager) puppetAwareProductionCmdFactory(name string, argValues genericexec.TemplateGetter, arg ...string) (*exec.Cmd, error) {
	// Pass arguments through the template engine.
	renderedArgs, err := genericexec.RenderArgTemplates(arg, argValues)
	if err != nil {
		return nil, err
	}

	// Do magic if the command is the keyword "puppet".
	if name == "puppet" {
		name = ctx.puppetConfig.PuppetExecutable
		// Need to pass these for non-root puppet cli to act on puppet master file locations :|
		origArg := renderedArgs
		renderedArgs = []string{origArg[0], "--config", ctx.puppetConfig.ConfFile, "--confdir", ctx.puppetConfig.ConfDir}
		renderedArgs = append(renderedArgs, origArg[1:]...)
	}

	cmd := exec.Command(name, renderedArgs...)
	return cmd, nil
}
