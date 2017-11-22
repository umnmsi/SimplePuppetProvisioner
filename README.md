# Simple Puppet Provisioner
[![Build Status](https://travis-ci.org/mbaynton/SimplePuppetProvisioner.svg?branch=master)](https://travis-ci.org/mbaynton/SimplePuppetProvisioner)

This software is designed to live on your [Puppet](https://puppet.com/) master to help automate the process of 
introducing new nodes to your infrastructure. In particular, it currently can help with
  * Signing your agent's certificates.
  * Informing an [ENC](https://puppet.com/docs/puppet/5.3/nodes_external.html#what-is-an-enc) which puppet environment a new hostname should get placed in.

It does this by offering a simple authenticated HTTP API that new nodes may call out to during their first boot.
You can interact with this daemon through `curl`, for example:
```bash
$ curl https://puppet.my.org:8240/provision -d hostname=newnode.my.org -d environment=production \
  --digest --user provision-user:SomeSuperSecretPassword  
```
The premise is that you either configure your base system images to run something like the above on
first boot, or add it to a provisioning system like cloud-init. When the Simple Puppet Provisioner software
receives an authenticated request, it will sign the certificate and/or set the environment for that
node.

## Configuration
The daemon will look for its configuration file in the working directory and in
the directory `/etc/spp`. Configuration may be specified as yaml, toml, json, and
other formats supported by [the viper configuration library](https://github.com/spf13/viper).

Example configuration filenames:
  * `/etc/spp/spp.conf.yml`, a systemwide configuration in yaml.
  * `./spp.conf.yml`, a local configuration in yaml.
  * `./spp.conf.json`, a local configuration in json.

Only the first configuration file found is read.

A complete example configuration in yaml is maintained [in the source git repository](https://github.com/mbaynton/SimplePuppetProvisioner/blob/master/spp.conf.yml).
Most functions the daemon is capable of are optional and will simply not be performed if left
unconfigured. These include
  * Logging, which only occurs if `LogFile` is set to a filename.
  * HTTP request authentication, which only occurs if an `HttpAuth` structure is present.
  * Notifications on IRC or Slack, which only occur if a `Notifications` structure is present.
  * Signing of agent's client certificates, which only occurs if a `CertSigning` structure is present.

## Monitoring
The daemon offers a simple report of internal statistics over its http interface at `/stats`.
It does not require any HTTP authentication and should always return an `HTTP 200`. It is therefore
suitable for use as the target of a primitive HTTP heartbeat health checker.

Development of this software was sponsored by the [Minnesota Supercomputing Institute](https://www.msi.umn.edu/).