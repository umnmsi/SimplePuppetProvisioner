# Simple Puppet Provisioner
[![Build Status](https://travis-ci.org/mbaynton/SimplePuppetProvisioner.svg?branch=master)](https://travis-ci.org/mbaynton/SimplePuppetProvisioner)

This software is designed to live on your [Puppet](https://puppet.com/) master to help automate the process of 
introducing new nodes to your infrastructure. In particular, it currently can help with
  * Signing agent certificates with a degree of security greater than turning on auto-signing.
  * Informing an [ENC](https://puppet.com/docs/puppet/5.3/nodes_external.html#what-is-an-enc) which puppet environment a new hostname should get placed in.

It does this by offering a simple authenticated HTTP API that new nodes may call out to during their first boot.
You could interact with this service through `curl`, for example:
```bash
$ curl https://puppet.my.org:8240/provision -d hostname=newnode.my.org -d tasks=cert,environment -d wait=environment \
-d environment=production --digest --user provision-user:SomeSuperSecretPassword  
```
The premise is that you either configure your base system images to run something like the above on
first boot, or add it to a provisioning system like cloud-init. When the Simple Puppet Provisioner software
receives an authenticated request, it will sign the certificate and/or set the environment for that
node.

## Requirements
The service is a statically-linked binary, so external dependencies / environmental requirements are minimal.
That said,
  * Testing is occurring only on Linux. YMMV on Windows; feel free to provide feedback if you use this
    software in a Windows environment.
  * Puppet must be installed (of course).
  * Any other tools you set up to run through `GenericExecTasks` must be installed (of course).

## Installation
  * TBD (should provide precompiled releases.)

## Starting and Stopping
The process can typically be started simply by executing it with no arguments. It should be run as the same
user that runs your puppet master server, user `puppet` on standard installations.  
It does not daemonize, so write initscripts accordingly.

The process should shut down cleanly in response to SIGTERMs.

## HTTP API
### /provision
#### Request
**Method: POST**  
**Content-Type: application/x-www-form-urlencoded**
<table border="1">
  <tr><th>Field</th><th>Required?</th><th>Example</th><th>Description</th></tr>
  <tr><td>hostname</td><td>required</td><td>foo.bar.com</td><td>The name of the host to be provisioned, as it will identify itself to puppet.</td></tr>
  <tr><td>tasks</td><td>required</td><td>cert,environment</td><td>Comma-separated list of provisioning operations to perform. Valid operations are the `Name`s defined in the `GenericExecTasks` configuration section, plus the special built-in task name `cert` to cause client certificate signing.</td></tr>
  <tr><td>waits</td><td>optional</td><td>environment</td><td>Comma-separated list of provisioning operations to wait for before the response is sent back. If you need to know the outcome of a provisioning operation, add it to this list and its results will be included in the response.</td></tr>  
  <tr><td>cert-revoke</td><td>optional</td><td>true</td><td>If set, any existing certificates for the same hostname will be reovked to enable successful signing of a new CSR for this hostname.</td></tr>
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