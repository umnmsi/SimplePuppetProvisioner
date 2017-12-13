#!/bin/sh
#
# This script accepts as arguments a hostname and associated environment. It commits this as a file readable by
# the simple ENC https://github.com/Zetten/puppet-hiera-enc.

set -e

LOCAL_CLONE=~/node-environments
HOSTNAME=$1
ENVIRONMENT=$2

if [ ! -d $LOCAL_CLONE/.git ]; then
  >&2 echo "Local git clone was not found at $LOCAL_CLONE; you must manually clone to this location once."
  exit 1
fi

if [ -z "$HOSTNAME" -o -z "$ENVIRONMENT" ]; then
  >&2 echo "Invalid input to set-environment.sh: A nonempty hostname and environment are required."
  exit 1
fi

cd $LOCAL_CLONE

git pull
echo "environment: $ENVIRONMENT" > "$HOSTNAME.yml"
git add "$HOSTNAME.yml"
git commit -m "environment: $ENVIRONMENT (automated commit for $HOSTNAME)"
git push

echo "Done."
