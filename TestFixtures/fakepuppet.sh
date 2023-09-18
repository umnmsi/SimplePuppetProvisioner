#!/bin/sh

if [ $# -eq 1 -a $1 = '--version' ]; then
  echo 6.26.0
else
  echo "signeddir = a
csrdir = b
ssldir = c
confdir = d
config = e
environmentpath = f"
fi