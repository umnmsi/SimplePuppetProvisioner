# Simple Puppet Provisioner
[![Build Status](https://travis-ci.org/mbaynton/SimplePuppetProvisioner.svg?branch=master)](https://travis-ci.org/mbaynton/SimplePuppetProvisioner)

## Configuration
The daemon will look for its configuration file in the working directory and in
the directory `/etc/spp`. Configuration may be specified as yaml, toml, json, and
other formats supported by [the viper configuration library](https://github.com/spf13/viper).

Example configuration filenames:
  * `/etc/spp/spp.conf.yml`, a systemwide configuration in yaml.
  * `./spp.conf.yml`, a local configuration in yaml.
  * `./spp.conf.json`, a local configuration in json.

A complete example configuration in yaml is maintained [in the source git repository](https://github.com/mbaynton/SimplePuppetProvisioner/blob/master/spp.conf.yml).


Development of this software was sponsored by the [Minnesota Supercomputing Institute](https://www.msi.umn.edu/).