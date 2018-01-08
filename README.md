# Simple Puppet Provisioner
[![Build Status](https://travis-ci.org/mbaynton/SimplePuppetProvisioner.svg?branch=master)](https://travis-ci.org/mbaynton/SimplePuppetProvisioner) [![Coverage Status](https://coveralls.io/repos/github/mbaynton/SimplePuppetProvisioner/badge.svg?branch=master)](https://coveralls.io/github/mbaynton/SimplePuppetProvisioner?branch=master)

This software is designed to live on your [Puppet](https://puppet.com/) master to automate some routine tasks:
  * It can listen for github webooks and run [r10k](https://puppet.com/docs/pe/2017.3/code_management/r10k.html) so your puppet manifests stay up-to-date with your git repositories.
    Some configurability is provided to run alternative commands in response to received webhooks, for example to
    also rsync changes to additional compile masters. It's basically a subset of functionality offered by [webhook](https://github.com/adnanh/webhook),
    but does not require you to run two services.
     
  * It can listen for requests sent during the first boot of new nodes in your infrastructure, and
      * Sign agent certificates for new nodes with a degree of security greater than turning on auto-signing. This task
        gets special treatment to handle the case / race condition where the first puppet run on the new node has not
        yet sent its certificate to the master. In that case, this software will watch for the certificate
        signing request to arrive from the agent and sign it as soon as it is available.
      * Run additional scripts or commands through an extensive, templatized invocation system. This can be used to 
        perform any custom actions your environment dictates for introduction of new nodes at your site. For example,
        this is used at [MSI](https://www.msi.umn.edu/) to inform a simple [ENC](https://puppet.com/docs/puppet/5.3/nodes_external.html#what-is-an-enc)
        which puppet environment the new node should get placed in.

    The premise for the "first-boot" listener is that you either directly configure your trusted system images to run 
    something like a `curl` call to this service on first boot, or have another provisioning system like cloud-init 
    kick off a request to this software. This software supports several methods of HTTP authentication to verify that
    the request is trustworthy. When it receives an authenticated request, it will sign the certificate and/or run
    commands.

If you use IRC or Slack, you can configure the software to send notifications to those platforms as it goes about its
tasks.

## Example `curl` call
This example call to the service using `curl` will cause the puppet master to sign a CSR for the host "newnode.my.org"
(immediately if the agent has already submitted it, or when it arrives otherwise), and some custom command from the 
configuration file called "environment" to be run as well. It will also submit some HTTP authentication credentials
using the digest method.
```bash
$ curl http://puppet.my.org:8240/provision -d hostname=newnode.my.org -d tasks=cert,environment \
--digest --user provision-user:SomeSuperSecretPassword  
```

## Requirements
The service is a statically-linked binary, so external dependencies / environmental requirements are minimal.
That said,
  * Testing is occurring only on Linux. YMMV on Windows; feel free to provide feedback if you use this
    software in a Windows environment.
  * Puppet must be installed (of course).
  * Any other tools you set up to run through `GenericExecTasks` must be installed (of course).

## Installation
If the precompiled binary runs on your platform, you can simply
  * Download the `SimplePuppetProvisioner` binary from the [latest release](https://github.com/mbaynton/SimplePuppetProvisioner/releases).
  * Create a configuration file for it. The [reference config file](https://github.com/mbaynton/SimplePuppetProvisioner/blob/master/spp.conf.yml)
    is a good starting point.
  * Done!

Precompiled binaries are currently being provided for Linux x86-64. If you need to run it on another platform,
install the [GoLang 1.8+ tools](https://golang.org/doc/install#install), place this git repository under your `GOPATH`,
and `go install` it.

## Starting and Stopping
The process can typically be started simply by executing it with no arguments. It should be run as the same
user that runs your puppet master server, user `puppet` on standard installations.  
It does not daemonize, so write initscripts accordingly.

The process should shut down cleanly in response to SIGTERMs.

## HTTP API Reference
### /provision
#### Request
**Method: POST**  
**Content-Type: application/x-www-form-urlencoded**
<table border="1">
  <tr><th>Field</th><th>Required?</th><th>Example</th><th>Description</th></tr>
  <tr><td>hostname</td><td>required</td><td>foo.bar.com</td><td>The name of the host to be provisioned, as it will identify itself to puppet.</td></tr>
  <tr><td>tasks</td><td>required</td><td>cert,environment</td><td>Comma-separated list of provisioning operations to perform. Valid operations are the `Name`s defined in the `GenericExecTasks` configuration section, plus the special built-in task name `cert` to cause client certificate signing.</td></tr>
  <tr><td>waits</td><td>optional</td><td>environment</td><td>Comma-separated list of provisioning operations to wait for before the response is sent back. If you need to know the outcome of a provisioning operation, add it to this list and its results will be included in the response.</td></tr>  
  <tr><td>cert-revoke</td><td>optional</td><td>true</td><td>If set, any existing certificates for the same hostname will be revoked to enable successful signing of a new CSR for this hostname.</td></tr>
</table>

Requested tasks are assumed to be independent of each other and are run concurrently. If you need
tasks to be run in a particular order, call the API multiple times with `waits` on the earlier tasks.

#### Response
**Content-Type: application/json**
A json object containing a key matching each of the tasks requested. The value of each task key is an
object with the following values:
<table border="1">
  <tr><th>key</th><th>type</th><th>description</th></tr>
  <tr><td>Complete</td><td>bool</td><td>Whether or not the task completed before this response object was sent back to the client.</td></tr>
  <tr><td>Success</td><td>bool</td><td><ul><li><em>When Complete is false</em>: indicates whether the task was initiated successfully. A false value is an assurance that the task will never complete successfully, but a true value is no assurance the task will eventually run to completion without encountering errors.</li><li><em>When Complete is true</em>: indicates whether the task was a success.</li></ul></td></tr>
  <tr><td>Message</td><td>string</td><td>An optional message with details about the task's outcome.</td></tr>
</table>

## Configuration
### File formats and location
The service will look for its configuration file in the working directory and in
the directory `/etc/spp`. Configuration may be specified as yaml, toml, json, and
other formats supported by [the viper configuration library](https://github.com/spf13/viper).

Example configuration filenames:
  * `/etc/spp/spp.conf.yml`, a systemwide configuration in yaml.
  * `./spp.conf.yml`, a local configuration in yaml.
  * `./spp.conf.json`, a local configuration in json.

Only the first configuration file found is read.

Alternatively, a specific configuration file can be given as an option to SimplePuppetProvisioner when it
is started:
```bash
$ ./SimplePuppetProvisioner --config /path/to/my/config.yml
```

### File contents
A commented example configuration in yaml is maintained [in the source git repository](https://github.com/mbaynton/SimplePuppetProvisioner/blob/master/spp.conf.yml).
Most functions the software is capable of are optional and will simply not be performed if left
unconfigured. These include
  * Logging, which only occurs if `LogFile` is set to a filename.
  * HTTP request authentication, which only occurs if an `HttpAuth` structure is present.
  * Notifications on IRC or Slack, which only occur if a `Notifications` structure is present.
  * Mapping of named tasks to commands to be executed on the puppet master, which are only available if
    a `GenericExecTasks` structure is present.

## Monitoring
The software offers a simple JSON report of internal statistics over its http interface at `/stats`.
It does not require any HTTP authentication and should always return an `HTTP 200`. It is therefore
suitable for use as the target of a primitive HTTP heartbeat health checker.

**Values in /stats json**
<table>
<tr><th>value</th><th>description</th></tr>
<tr><td>uptime</td><td>The time that the SimplePuppetProvisioner process has been running, as a string with (h)ours/(m)inutes/(s)econds. Example: 31h44m2.023s</td></tr>
<tr><td>cert-signing-backlog</td><td>The number of calls that need to be made to puppet cert sign but are queued waiting on other signing operations to complete. Signing operations are not run concurrently.</td></tr>
</table>

- - -
Development of this software was sponsored by the [Minnesota Supercomputing Institute](https://www.msi.umn.edu/).